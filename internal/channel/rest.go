package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/health"
	"github.com/DonScott603/gogoclaw/internal/storage"
	"github.com/DonScott603/gogoclaw/pkg/types"
)

// RESTChannel implements Channel as an HTTP server.
type RESTChannel struct {
	cfg      config.ChannelConfig
	engine   *engine.Engine
	store    *storage.Store
	monitor  *health.Monitor
	inboxDir string
	apiKey   string

	mu      sync.Mutex
	handler func(ctx context.Context, msg types.InboundMessage)
	server  *http.Server
}

// RESTConfig holds the dependencies for creating a REST channel.
type RESTConfig struct {
	Channel  config.ChannelConfig
	Engine   *engine.Engine
	Store    *storage.Store
	Monitor  *health.Monitor
	InboxDir string
}

// NewREST creates a new REST API channel.
func NewREST(cfg RESTConfig) *RESTChannel {
	apiKey := ""
	if cfg.Channel.APIKeyEnv != "" {
		apiKey = os.Getenv(cfg.Channel.APIKeyEnv)
	}

	listen := cfg.Channel.Listen
	if listen == "" {
		listen = "127.0.0.1:8080"
	}

	rc := &RESTChannel{
		cfg:      cfg.Channel,
		engine:   cfg.Engine,
		store:    cfg.Store,
		monitor:  cfg.Monitor,
		inboxDir: cfg.InboxDir,
		apiKey:   apiKey,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/message", rc.handleMessage)
	mux.HandleFunc("GET /api/health", rc.handleHealth)
	mux.HandleFunc("GET /api/conversations", rc.handleConversations)

	rc.server = &http.Server{
		Addr:    listen,
		Handler: rc.authMiddleware(mux),
	}

	return rc
}

// Name returns the channel name.
func (rc *RESTChannel) Name() string { return "rest" }

// Start begins listening for HTTP requests.
func (rc *RESTChannel) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		rc.Stop(context.Background())
	}()
	err := rc.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("channel: rest: %w", err)
}

// Stop gracefully shuts down the HTTP server.
func (rc *RESTChannel) Stop(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return rc.server.Shutdown(shutdownCtx)
}

// Send is not used by the REST channel (responses are synchronous).
func (rc *RESTChannel) Send(_ context.Context, _ string, _ types.OutboundMessage) error {
	return nil
}

// OnMessage registers a handler for inbound messages.
func (rc *RESTChannel) OnMessage(handler func(ctx context.Context, msg types.InboundMessage)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.handler = handler
}

// authMiddleware validates the API key if one is configured.
func (rc *RESTChannel) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rc.apiKey != "" {
			auth := r.Header.Get("Authorization")
			expected := "Bearer " + rc.apiKey
			if auth != expected {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// messageRequest is the JSON body for POST /api/message.
type messageRequest struct {
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
}

// messageResponse is the JSON response from POST /api/message.
type messageResponse struct {
	ConversationID string   `json:"conversation_id"`
	Response       string   `json:"response"`
	Files          []string `json:"files,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (rc *RESTChannel) handleMessage(w http.ResponseWriter, r *http.Request) {
	var req messageRequest
	var savedFiles []string

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32 MB max
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "channel: rest: parse multipart: " + err.Error()})
			return
		}
		req.ConversationID = r.FormValue("conversation_id")
		req.Text = r.FormValue("text")

		// Save uploaded files to inbox.
		if rc.inboxDir != "" && r.MultipartForm != nil {
			for _, headers := range r.MultipartForm.File {
				for _, fh := range headers {
					saved, err := rc.saveUpload(fh)
					if err != nil {
						writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "channel: rest: save upload: " + err.Error()})
						return
					}
					savedFiles = append(savedFiles, saved)
				}
			}
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "channel: rest: invalid JSON: " + err.Error()})
			return
		}
	}

	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "text is required"})
		return
	}

	if req.ConversationID == "" {
		req.ConversationID = fmt.Sprintf("rest-%d", time.Now().UnixNano())
	}

	// Notify the inbound handler if registered.
	rc.mu.Lock()
	handler := rc.handler
	rc.mu.Unlock()
	if handler != nil {
		handler(r.Context(), types.InboundMessage{
			ConversationID: req.ConversationID,
			Text:           req.Text,
			Channel:        "rest",
			Timestamp:      time.Now(),
		})
	}

	// Send to engine and get response.
	resp, err := rc.engine.Send(r.Context(), req.Text)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "channel: rest: engine: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, messageResponse{
		ConversationID: req.ConversationID,
		Response:       resp,
		Files:          savedFiles,
	})
}

func (rc *RESTChannel) saveUpload(fh *multipart.FileHeader) (string, error) {
	src, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open uploaded file: %w", err)
	}
	defer src.Close()

	// Sanitize filename: strip path components.
	name := filepath.Base(fh.Filename)
	if name == "." || name == "/" {
		name = "upload"
	}

	destPath := filepath.Join(rc.inboxDir, name)
	dst, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create destination: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return name, nil
}

type healthResponse struct {
	Status     string                   `json:"status"`
	PIIMode    string                   `json:"pii_mode"`
	Components []componentStatusPayload `json:"components"`
}

type componentStatusPayload struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Details   string `json:"details"`
	LastCheck string `json:"last_check,omitempty"`
}

func (rc *RESTChannel) handleHealth(w http.ResponseWriter, r *http.Request) {
	components := rc.monitor.Status()
	overall := "healthy"
	payloads := make([]componentStatusPayload, len(components))
	for i, c := range components {
		payloads[i] = componentStatusPayload{
			Name:    c.Name,
			Status:  string(c.Status),
			Details: c.Details,
		}
		if !c.LastCheck.IsZero() {
			payloads[i].LastCheck = c.LastCheck.Format(time.RFC3339)
		}
		if c.Status == health.StatusUnhealthy {
			overall = "unhealthy"
		} else if c.Status == health.StatusDegraded && overall == "healthy" {
			overall = "degraded"
		}
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:     overall,
		PIIMode:    rc.monitor.PIIMode(),
		Components: payloads,
	})
}

type conversationPayload struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Agent     string `json:"agent"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (rc *RESTChannel) handleConversations(w http.ResponseWriter, r *http.Request) {
	convos, err := rc.store.ListConversations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "channel: rest: list conversations: " + err.Error()})
		return
	}

	payloads := make([]conversationPayload, len(convos))
	for i, c := range convos {
		payloads[i] = conversationPayload{
			ID:        c.ID,
			Title:     c.Title,
			Agent:     c.Agent,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
			UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
		}
	}

	writeJSON(w, http.StatusOK, payloads)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
