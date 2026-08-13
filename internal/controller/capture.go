package controller

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spsp4755/load-observatory/internal/core"
)

const maxCaptureInspectionBytes = 8 << 20

type captureRequest struct {
	MaxTokens int             `json:"max_tokens"`
	Messages  json.RawMessage `json:"messages"`
}

type captureResponse struct {
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"message"`
		Delta struct {
			Content   any               `json:"content"`
			ToolCalls []json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

func (s *Server) captureCompletion(w http.ResponseWriter, r *http.Request) {
	tokenHash, enabled, configured := s.captureCredentials()
	if !configured || !enabled {
		http.Error(w, "capture proxy is disabled", http.StatusServiceUnavailable)
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(hashCaptureToken(provided)), []byte(tokenHash)) != 1 {
		http.Error(w, "invalid capture token", http.StatusUnauthorized)
		return
	}
	targetID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/capture/"), "/v1/chat/completions")
	if targetID == "" || r.URL.Path != "/capture/"+targetID+"/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	target, ok := s.store.GetTarget(targetID)
	if !ok || target.Type != core.TargetTypeModel {
		http.Error(w, "model target not found", http.StatusNotFound)
		return
	}
	rawSession := strings.TrimSpace(r.Header.Get("X-Load-Observatory-Session"))
	if rawSession == "" || len(rawSession) > 256 {
		http.Error(w, "X-Load-Observatory-Session is required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
	if err != nil {
		http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	var input captureRequest
	if json.Unmarshal(body, &input) != nil {
		http.Error(w, "invalid OpenAI request", http.StatusBadRequest)
		return
	}
	started := time.Now()
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "invalid target URL", http.StatusBadGateway)
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", r.Header.Get("Accept"))
	if target.APIKey != "" {
		upstream.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	response, err := s.proxyClient.Do(upstream)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		s.recordCapture(targetID, rawSession, r, started, input, nil, core.CaptureEvent{}, http.StatusBadGateway, 0)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		if strings.EqualFold(key, "Connection") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	var inspected bytes.Buffer
	limited := &limitedCaptureWriter{dst: &inspected, remaining: maxCaptureInspectionBytes}
	observed := core.CaptureEvent{}
	firstByte := int64(0)
	if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), 16<<20)
		flusher, _ := w.(http.Flusher)
		for scanner.Scan() {
			parseCaptureResponse(scanner.Bytes(), &observed)
			if firstByte == 0 && captureLineHasOutput(scanner.Bytes()) {
				firstByte = time.Since(started).Milliseconds()
			}
			line := append(append([]byte(nil), scanner.Bytes()...), '\n')
			_, _ = w.Write(line)
			_, _ = limited.Write(line)
			if flusher != nil {
				flusher.Flush()
			}
		}
	} else {
		firstByte = time.Since(started).Milliseconds()
		_, _ = io.Copy(io.MultiWriter(w, limited), response.Body)
	}
	s.recordCapture(targetID, rawSession, r, started, input, inspected.Bytes(), observed, int64(response.StatusCode), firstByte)
}

type captureSettingsRequest struct {
	Enabled              bool    `json:"enabled"`
	Token                string  `json:"token"`
	SessionIdleMinutes   int     `json:"session_idle_minutes"`
	MaxEventsPerSession  int     `json:"max_events_per_session"`
	RetentionSessions    int     `json:"retention_sessions"`
	DefaultReplayVUs     int     `json:"default_replay_vus"`
	ReplayThinkTimeScale float64 `json:"replay_think_time_scale"`
	ReplayBufferSeconds  int     `json:"replay_buffer_seconds"`
	ReplayDrainSeconds   int     `json:"replay_drain_seconds"`
}

func hashCaptureToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *Server) captureCredentials() (string, bool, bool) {
	settings := s.store.GetCaptureSettings()
	tokenHash := settings.TokenHash
	if tokenHash == "" {
		tokenHash = s.envCaptureTokenHash
	}
	enabled := tokenHash != ""
	if settings.Configured {
		enabled = settings.Enabled && tokenHash != ""
	}
	return tokenHash, enabled, tokenHash != ""
}

func (s *Server) getCaptureSettings(w http.ResponseWriter) {
	settings := s.store.GetCaptureSettings()
	_, enabled, configured := s.captureCredentials()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": enabled, "token_configured": configured,
		"session_idle_minutes": settings.SessionIdleMinutes, "max_events_per_session": settings.MaxEventsPerSession,
		"retention_sessions": settings.RetentionSessions, "default_replay_vus": settings.DefaultReplayVUs,
		"replay_think_time_scale": settings.ReplayThinkTimeScale, "replay_buffer_seconds": settings.ReplayBufferSeconds,
		"replay_drain_seconds": settings.ReplayDrainSeconds,
	})
}

func (s *Server) updateCaptureSettings(w http.ResponseWriter, r *http.Request) {
	var input captureSettingsRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if input.SessionIdleMinutes < 1 || input.SessionIdleMinutes > 120 || input.MaxEventsPerSession < 8 || input.MaxEventsPerSession > 128 || input.RetentionSessions < 10 || input.RetentionSessions > 500 || input.DefaultReplayVUs < 1 || input.DefaultReplayVUs > core.MaxVUs || input.ReplayThinkTimeScale < 0.1 || input.ReplayThinkTimeScale > 10 || input.ReplayBufferSeconds < 30 || input.ReplayBufferSeconds > 1800 || input.ReplayDrainSeconds < 0 || input.ReplayDrainSeconds > 600 {
		http.Error(w, "capture setting is out of range", http.StatusBadRequest)
		return
	}
	current := s.store.GetCaptureSettings()
	if token := strings.TrimSpace(input.Token); token != "" {
		if len(token) < 24 || len(token) > 256 {
			http.Error(w, "capture token must be 24 to 256 characters", http.StatusBadRequest)
			return
		}
		current.TokenHash = hashCaptureToken(token)
	}
	if input.Enabled && current.TokenHash == "" && s.envCaptureTokenHash == "" {
		http.Error(w, "set a capture token before enabling the proxy", http.StatusBadRequest)
		return
	}
	current.Enabled, current.SessionIdleMinutes, current.MaxEventsPerSession, current.RetentionSessions = input.Enabled, input.SessionIdleMinutes, input.MaxEventsPerSession, input.RetentionSessions
	current.DefaultReplayVUs, current.ReplayThinkTimeScale, current.ReplayBufferSeconds, current.ReplayDrainSeconds = input.DefaultReplayVUs, input.ReplayThinkTimeScale, input.ReplayBufferSeconds, input.ReplayDrainSeconds
	s.store.SetCaptureSettings(current)
	s.getCaptureSettings(w)
}

func captureLineHasOutput(line []byte) bool {
	line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	var decoded captureResponse
	if json.Unmarshal(line, &decoded) != nil {
		return false
	}
	for _, choice := range decoded.Choices {
		if len(choice.Delta.ToolCalls) > 0 {
			return true
		}
		if text, ok := choice.Delta.Content.(string); ok && text != "" {
			return true
		}
	}
	return false
}

func (s *Server) recordCapture(targetID, rawSession string, r *http.Request, started time.Time, input captureRequest, responseBody []byte, observed core.CaptureEvent, status, ttft int64) {
	digest := hmac.New(sha256.New, s.captureSalt)
	_, _ = digest.Write([]byte(targetID + "\x00" + rawSession))
	id := "capture-" + hex.EncodeToString(digest.Sum(nil)[:10])
	now := time.Now()
	client := "OpenAI-compatible client"
	switch raw := strings.ToLower(r.Header.Get("X-Load-Observatory-Client")); {
	case strings.Contains(raw, "qwen"):
		client = "Qwen Code"
	case strings.Contains(raw, "opencode"):
		client = "OpenCode"
	}
	event := core.CaptureEvent{PromptTokens: approximatePromptTokens(input.Messages), MaxTokens: input.MaxTokens, StatusCode: int(status), LatencyMillis: now.Sub(started).Milliseconds(), TTFTMillis: ttft}
	if observed.PromptTokens > 0 {
		event.PromptTokens = observed.PromptTokens
	}
	event.OutputTokens, event.ToolCalls, event.FinishReason = observed.OutputTokens, observed.ToolCalls, observed.FinishReason
	if observed.PromptTokens == 0 && observed.OutputTokens == 0 {
		parseCaptureResponse(responseBody, &event)
	}
	s.store.RecordCapture(core.CaptureSession{ID: id, TargetID: targetID, Client: client, StartedUnixMillis: started.UnixMilli(), UpdatedUnixMillis: now.UnixMilli()}, event)
}

func approximatePromptTokens(raw json.RawMessage) int {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	return (countStringRunes(value) + 3) / 4
}
func countStringRunes(value any) int {
	switch item := value.(type) {
	case string:
		return utf8.RuneCountInString(item)
	case []any:
		total := 0
		for _, child := range item {
			total += countStringRunes(child)
		}
		return total
	case map[string]any:
		total := 0
		for key, child := range item {
			if key == "content" || key == "text" || key == "arguments" {
				total += countStringRunes(child)
			}
		}
		return total
	default:
		return 0
	}
}

func parseCaptureResponse(raw []byte, event *core.CaptureEvent) {
	consume := func(data []byte) {
		var decoded captureResponse
		if json.Unmarshal(data, &decoded) != nil {
			return
		}
		if decoded.Usage.PromptTokens > 0 {
			event.PromptTokens = decoded.Usage.PromptTokens
		}
		if decoded.Usage.CompletionTokens > 0 {
			event.OutputTokens = decoded.Usage.CompletionTokens
		}
		for _, choice := range decoded.Choices {
			if choice.FinishReason != "" {
				event.FinishReason = choice.FinishReason
			}
			event.ToolCalls += len(choice.Message.ToolCalls) + len(choice.Delta.ToolCalls)
		}
	}
	if bytes.Contains(raw, []byte("data:")) {
		for _, line := range bytes.Split(raw, []byte("\n")) {
			line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(line) > 0 && !bytes.Equal(line, []byte("[DONE]")) {
				consume(line)
			}
		}
	} else {
		consume(raw)
	}
}

type limitedCaptureWriter struct {
	dst       io.Writer
	remaining int64
}

func (w *limitedCaptureWriter) Write(p []byte) (int, error) {
	original := len(p)
	if w.remaining <= 0 {
		return original, nil
	}
	if int64(len(p)) > w.remaining {
		p = p[:w.remaining]
	}
	n, err := w.dst.Write(p)
	w.remaining -= int64(n)
	if err != nil {
		return n, fmt.Errorf("capture response metadata: %w", err)
	}
	return original, nil
}
