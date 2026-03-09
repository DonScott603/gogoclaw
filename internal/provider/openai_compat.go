package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompat is a client for any OpenAI-compatible API (OpenAI, MiniMax, Groq, etc.).
type OpenAICompat struct {
	name       string
	baseURL    string
	apiKey     string
	model      string
	maxContext int
	client     *http.Client
}

// OpenAICompatConfig holds the parameters needed to create an OpenAICompat provider.
type OpenAICompatConfig struct {
	Name             string
	BaseURL          string
	APIKey           string
	DefaultModel     string
	MaxContextTokens int
	Timeout          time.Duration
}

// NewOpenAICompat creates a new OpenAI-compatible provider.
func NewOpenAICompat(cfg OpenAICompatConfig) *OpenAICompat {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &OpenAICompat{
		name:       cfg.Name,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		model:      cfg.DefaultModel,
		maxContext:  cfg.MaxContextTokens,
		client:     &http.Client{Timeout: timeout},
	}
}

func (o *OpenAICompat) Name() string { return o.name }

func (o *OpenAICompat) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/models", nil)
	if err != nil {
		return false
	}
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (o *OpenAICompat) CountTokens(content string) (int, error) {
	// Rough estimate: ~4 chars per token for English text.
	return len(content) / 4, nil
}

// openaiRequest is the request body for the chat completions endpoint.
type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Tools       []Tool          `json:"tools,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Stream      bool            `json:"stream"`
}

// openaiResponse is the response body from the chat completions endpoint.
type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openaiChoice struct {
	Message struct {
		Content   string           `json:"content"`
		ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
	} `json:"message"`
	Delta struct {
		Content   string           `json:"content"`
		ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openaiMessage is the OpenAI wire format for messages, which differs from our
// internal Message type in how tool_calls are structured (nested under "function").
type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// convertMessages transforms internal Messages to the OpenAI wire format.
func convertMessages(msgs []Message) []openaiMessage {
	out := make([]openaiMessage, len(msgs))
	for i, m := range msgs {
		om := openaiMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, openaiToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		out[i] = om
	}
	return out
}

func (o *OpenAICompat) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := o.buildRequest(req, false)
	respBody, err := o.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	var oResp openaiResponse
	if err := json.NewDecoder(respBody).Decode(&oResp); err != nil {
		return nil, fmt.Errorf("provider: %s: decode response: %w", o.name, err)
	}

	resp := &ChatResponse{
		Usage: TokenUsage{
			PromptTokens:     oResp.Usage.PromptTokens,
			CompletionTokens: oResp.Usage.CompletionTokens,
			TotalTokens:      oResp.Usage.TotalTokens,
		},
	}
	if len(oResp.Choices) > 0 {
		choice := oResp.Choices[0]
		resp.Content = choice.Message.Content
		for _, tc := range choice.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
	}
	return resp, nil
}

func (o *OpenAICompat) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	body := o.buildRequest(req, true)
	respBody, err := o.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		defer respBody.Close()
		o.parseSSE(ctx, respBody, ch)
	}()
	return ch, nil
}

func (o *OpenAICompat) buildRequest(req ChatRequest, stream bool) openaiRequest {
	model := req.Model
	if model == "" {
		model = o.model
	}
	body := openaiRequest{
		Model:    model,
		Messages: convertMessages(req.Messages),
		Tools:    req.Tools,
		Stream:   stream,
	}
	if req.MaxTokens > 0 {
		body.MaxTokens = req.MaxTokens
	}
	if req.Temperature > 0 {
		t := req.Temperature
		body.Temperature = &t
	}
	return body
}

func (o *OpenAICompat) doRequest(ctx context.Context, body openaiRequest) (io.ReadCloser, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("provider: %s: marshal request: %w", o.name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("provider: %s: create request: %w", o.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("provider: %s: request: %w", o.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("provider: %s: HTTP %d: %s", o.name, resp.StatusCode, string(errBody))
	}
	return resp.Body, nil
}

func (o *OpenAICompat) parseSSE(ctx context.Context, r io.Reader, ch chan<- StreamChunk) {
	scanner := bufio.NewScanner(r)
	// Accumulate tool call argument fragments by index.
	toolCalls := make(map[int]*ToolCall)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- StreamChunk{Done: true, Error: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Flush accumulated tool calls.
			if len(toolCalls) > 0 {
				var tcs []ToolCall
				for _, tc := range toolCalls {
					tcs = append(tcs, *tc)
				}
				ch <- StreamChunk{ToolCalls: tcs, Timestamp: time.Now()}
			}
			ch <- StreamChunk{Done: true, Timestamp: time.Now()}
			return
		}

		var oResp openaiResponse
		if err := json.Unmarshal([]byte(data), &oResp); err != nil {
			continue
		}
		if len(oResp.Choices) == 0 {
			continue
		}
		delta := oResp.Choices[0].Delta

		if delta.Content != "" {
			ch <- StreamChunk{Content: delta.Content, Timestamp: time.Now()}
		}

		for _, tc := range delta.ToolCalls {
			idx := 0 // default index
			if existing, ok := toolCalls[idx]; ok {
				existing.Arguments = json.RawMessage(
					string(existing.Arguments) + tc.Function.Arguments,
				)
			} else {
				toolCalls[idx] = &ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: json.RawMessage(tc.Function.Arguments),
				}
			}
		}
	}
}
