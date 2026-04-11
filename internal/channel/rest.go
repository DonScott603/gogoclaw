package channel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DonScott603/gogoclaw/internal/audit"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/health"
	"github.com/DonScott603/gogoclaw/internal/storage"
	"github.com/DonScott603/gogoclaw/pkg/types"

	"github.com/google/uuid"
)

// RESTChannel implements Channel as an HTTP server.
type RESTChannel struct {
	cfg            config.ChannelConfig
	engine         *engine.Engine
	sessionManager *engine.SessionManager
	store          *storage.Store
	monitor        *health.Monitor
	auditLogger    *audit.Logger
	inboxDir       string
	apiKey         string
	allowedOrigins map[string]bool
	rateLimiter    *rateLimiter

	mu      sync.Mutex
	handler func(ctx context.Context, msg types.InboundMessage)
	server  *http.Server
}

// RESTConfig holds the dependencies for creating a REST channel.
type RESTConfig struct {
	Channel        config.ChannelConfig
	Engine         *engine.Engine
	SessionManager *engine.SessionManager
	Store          *storage.Store
	Monitor        *health.Monitor
	AuditLogger    *audit.Logger
	InboxDir       string
	RateLimit      int // requests per minute, 0 = use default (60)
}

// NewREST creates a new REST API channel.
func NewREST(cfg RESTConfig) (*RESTChannel, error) {
	apiKey, err := resolveAPIKey(cfg.Channel)
	if err != nil {
		return nil, fmt.Errorf("channel: rest: %w", err)
	}

	listen := cfg.Channel.Listen
	if listen == "" {
		listen = "127.0.0.1:8080"
	}

	origins := make(map[string]bool, len(cfg.Channel.AllowedOrigins))
	for _, o := range cfg.Channel.AllowedOrigins {
		origins[o] = true
	}

	rateLimit := cfg.RateLimit
	if rateLimit <= 0 {
		rateLimit = 60
	}

	rc := &RESTChannel{
		cfg:            cfg.Channel,
		engine:         cfg.Engine,
		sessionManager: cfg.SessionManager,
		store:          cfg.Store,
		monitor:        cfg.Monitor,
		auditLogger:    cfg.AuditLogger,
		inboxDir:       cfg.InboxDir,
		apiKey:         apiKey,
		allowedOrigins: origins,
		rateLimiter:    newRateLimiter(rateLimit),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/message", rc.handleMessage)
	mux.HandleFunc("GET /api/health", rc.handleHealth)
	mux.HandleFunc("GET /api/conversations", rc.handleConversations)

	rc.server = &http.Server{
		Addr:    listen,
		Handler: rc.corsMiddleware(rc.authMiddleware(rc.rateLimitMiddleware(rc.auditMiddleware(mux)))),
	}

	return rc, nil
}

// APIKey returns the resolved (or auto-generated) API key.
func (rc *RESTChannel) APIKey() string { return rc.apiKey }

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

// resolveAPIKey determines the API key from config, env var, or auto-generates one.
func resolveAPIKey(cfg config.ChannelConfig) (string, error) {
	// Literal key in config takes priority.
	if cfg.APIKey != "" {
		return cfg.APIKey, nil
	}
	// Env var is second priority.
	if cfg.APIKeyEnv != "" {
		if key := os.Getenv(cfg.APIKeyEnv); key != "" {
			return key, nil
		}
	}
	// Auto-generate a random key.
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate API key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// --- middleware ---

// corsMiddleware handles CORS based on configured allowed origins.
func (rc *RESTChannel) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && len(rc.allowedOrigins) > 0 && rc.allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authMiddleware validates the API key if one is configured.
func (rc *RESTChannel) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rc.apiKey != "" {
			auth := r.Header.Get("Authorization")
			expected := "Bearer " + rc.apiKey
			if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) != 1 {
				writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware enforces per-API-key rate limiting.
func (rc *RESTChannel) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		if key == "" {
			key = "_anonymous"
		}
		retryAfter := rc.rateLimiter.allow(key)
		if retryAfter > 0 {
			writeJSON(w, http.StatusTooManyRequests, rateLimitResponse{
				Error:      "rate limit exceeded",
				RetryAfter: int(retryAfter.Seconds()) + 1,
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// auditMiddleware logs every REST request to the audit trail.
func (rc *RESTChannel) auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		if rc.auditLogger != nil {
			fields := map[string]string{
				"method":      r.Method,
				"path":        r.URL.Path,
				"status":      strconv.Itoa(sw.status),
				"duration_ms": strconv.FormatInt(time.Since(start).Milliseconds(), 10),
				"client_ip":   r.RemoteAddr,
			}
			if cid := r.URL.Query().Get("conversation_id"); cid != "" {
				fields["conversation_id"] = cid
			}
			rc.auditLogger.Log(audit.EventRESTRequest, fields)
		}
	})
}

// statusWriter captures the HTTP status code for audit logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// --- request/response types ---

type messageRequest struct {
	ConversationID string `json:"conversation_id"`
	Text           string `json:"text"`
}

type messageResponse struct {
	ConversationID string   `json:"conversation_id"`
	Response       string   `json:"response"`
	Files          []string `json:"files,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type rateLimitResponse struct {
	Error      string `json:"error"`
	RetryAfter int    `json:"retry_after"`
}

// --- handlers ---

func (rc *RESTChannel) handleMessage(w http.ResponseWriter, r *http.Request) {
	var req messageRequest
	var savedFiles []string

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "channel: rest: parse multipart: " + err.Error()})
			return
		}
		req.ConversationID = r.FormValue("conversation_id")
		req.Text = r.FormValue("text")

		if rc.inboxDir != "" && r.MultipartForm != nil {
			for _, headers := range r.MultipartForm.File {
				for _, fh := range headers {
					if fh.Size > 32<<20 {
						writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: fmt.Sprintf("channel: rest: file %q exceeds 32 MB limit", fh.Filename)})
						return
					}
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
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit for JSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "channel: rest: invalid JSON: " + err.Error()})
			return
		}
	}

	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "text is required"})
		return
	}

	// Validate conversation_id.
	if req.ConversationID != "" {
		if len(req.ConversationID) > 128 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "conversation_id must be at most 128 characters"})
			return
		}
	} else {
		req.ConversationID = uuid.New().String()
	}

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

	// Prepend file upload context so the LLM knows how to access them.
	filePrefix := ""
	if len(savedFiles) > 0 {
		var parts []string
		for _, f := range savedFiles {
			parts = append(parts, "inbox/"+f)
		}
		filePrefix = "[Files uploaded: " + strings.Join(parts, ", ") + "] You can read them with file_read using paths relative to the workspace. "
	}

	session, err := rc.sessionManager.GetOrCreate(r.Context(), "rest", req.ConversationID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "channel: rest: session: " + err.Error()})
		return
	}
	prompt := "[Channel: REST API] " + filePrefix + req.Text
	resp, err := rc.engine.Send(r.Context(), session, prompt)
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

	base := filepath.Base(fh.Filename)
	if base == "." || base == "/" {
		base = "upload"
	}
	name := fmt.Sprintf("%d_%s", time.Now().UnixNano(), base)

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
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	convos, err := rc.store.ListConversationsPaged(r.Context(), limit, offset)
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

// --- rate limiter ---

// rateLimiter implements a token bucket rate limiter keyed by API key string.
type rateLimiter struct {
	maxTokens  int
	refillRate float64 // tokens per second
	buckets    map[string]*bucket
	mu         sync.Mutex
}

type bucket struct {
	tokens   float64
	lastTime time.Time
}

func newRateLimiter(requestsPerMinute int) *rateLimiter {
	return &rateLimiter{
		maxTokens:  requestsPerMinute,
		refillRate: float64(requestsPerMinute) / 60.0,
		buckets:    make(map[string]*bucket),
	}
}

// allow checks if a request is allowed for the given key.
// Returns 0 if allowed, or the duration to wait before retrying.
func (rl *rateLimiter) allow(key string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{
			tokens:   float64(rl.maxTokens),
			lastTime: now,
		}
		rl.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * rl.refillRate
	if b.tokens > float64(rl.maxTokens) {
		b.tokens = float64(rl.maxTokens)
	}
	b.lastTime = now

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return 0
	}

	// Calculate wait time until one token is available.
	deficit := 1.0 - b.tokens
	waitSeconds := deficit / rl.refillRate
	return time.Duration(waitSeconds * float64(time.Second))
}
