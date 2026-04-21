package eino_llm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	log "xiaozhi-esp32-server-golang/logger"
)

const (
	defaultThinkingEffort     = "medium"
	reasoningContentMarker    = "\"reasoning_content\""
	reasoningTrackerConfigKey = "__reasoning_content_tracker"
	reasoningDetectConfigKey  = "__enable_reasoning_content_detection"
	reasoningDetectTailSize   = 1024
)

type thinkingConfig struct {
	Mode          string `json:"mode"`
	BudgetTokens  *int   `json:"budget_tokens,omitempty"`
	Effort        string `json:"effort,omitempty"`
	ClearThinking *bool  `json:"clear_thinking,omitempty"`
}

type openAICompatibleConfig struct {
	Type        string          `json:"type"`
	Provider    string          `json:"provider"`
	ModelName   string          `json:"model_name"`
	APIKey      string          `json:"api_key"`
	BaseURL     string          `json:"base_url"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float32        `json:"temperature,omitempty"`
	TopP        *float32        `json:"top_p,omitempty"`
	Streamable  *bool           `json:"streamable,omitempty"`
	APIVersion  string          `json:"api_version,omitempty"`
	Thinking    *thinkingConfig `json:"thinking,omitempty"`
}

func (t thinkingConfig) enabled() bool {
	return strings.TrimSpace(t.Mode) != "" && t.Mode != "default"
}

func decodeConfigMap(input map[string]interface{}, target interface{}) error {
	if len(input) == 0 {
		return nil
	}

	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}

	return json.Unmarshal(payload, target)
}

func decodeOpenAICompatibleConfig(config map[string]interface{}) (openAICompatibleConfig, error) {
	var parsed openAICompatibleConfig
	err := decodeConfigMap(config, &parsed)
	if err != nil {
		return openAICompatibleConfig{}, err
	}

	parsed.Provider = strings.ToLower(strings.TrimSpace(parsed.Provider))
	parsed.Type = strings.ToLower(strings.TrimSpace(parsed.Type))
	parsed.ModelName = strings.TrimSpace(parsed.ModelName)
	parsed.APIKey = strings.TrimSpace(parsed.APIKey)
	parsed.BaseURL = strings.TrimSpace(parsed.BaseURL)
	parsed.APIVersion = strings.TrimSpace(parsed.APIVersion)
	parsed.Thinking = normalizeThinkingConfig(parsed.Thinking)

	return parsed, nil
}

func normalizeThinkingConfig(raw *thinkingConfig) *thinkingConfig {
	if raw == nil {
		return nil
	}

	normalized := &thinkingConfig{
		Mode:          strings.ToLower(strings.TrimSpace(raw.Mode)),
		BudgetTokens:  raw.BudgetTokens,
		Effort:        strings.ToLower(strings.TrimSpace(raw.Effort)),
		ClearThinking: raw.ClearThinking,
	}

	if normalized.Mode == "" && normalized.BudgetTokens == nil && normalized.Effort == "" && normalized.ClearThinking == nil {
		return nil
	}

	return normalized
}

type thinkingRoundTripper struct {
	base     http.RoundTripper
	provider string
	model    string
	thinking thinkingConfig
	tracker  *reasoningContentTracker
}

type loggingResponseReadCloser struct {
	io.ReadCloser
	provider   string
	model      string
	method     string
	url        string
	statusCode int
	body       bytes.Buffer
	logOnce    sync.Once
}

type reasoningContentTracker struct {
	returned atomic.Bool
}

func (t *reasoningContentTracker) MarkReturned() {
	if t != nil {
		t.returned.Store(true)
	}
}

func (t *reasoningContentTracker) HasReturned() bool {
	return t != nil && t.returned.Load()
}

func (t *reasoningContentTracker) Reset() {
	if t != nil {
		t.returned.Store(false)
	}
}

type reasoningDetectReadCloser struct {
	io.ReadCloser
	tracker *reasoningContentTracker
	tail    string
}

func (r *reasoningDetectReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.tracker != nil && !r.tracker.HasReturned() {
		chunk := r.tail + string(p[:n])
		if content, ok := extractNonEmptyReasoningContent(chunk); ok {
			r.tracker.MarkReturned()
			_ = content
		}
		if len(chunk) > reasoningDetectTailSize {
			r.tail = chunk[len(chunk)-reasoningDetectTailSize:]
		} else {
			r.tail = chunk
		}
	}
	return n, err
}

func (r *loggingResponseReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		_, _ = r.body.Write(p[:n])
	}
	if err != nil {
		if err == io.EOF {
			r.log("completed", nil)
		} else {
			r.log("read_error", err)
		}
	}
	return n, err
}

func (r *loggingResponseReadCloser) Close() error {
	r.log("closed", nil)
	return r.ReadCloser.Close()
}

func (r *loggingResponseReadCloser) log(stage string, err error) {
	r.logOnce.Do(func() {
		if err != nil {
			log.Warnf("[Eino-LLM][OpenAI-Raw][Response] provider=%s model=%s method=%s url=%s status=%d stage=%s err=%v raw=%s",
				r.provider, r.model, r.method, r.url, r.statusCode, stage, err, r.body.String())
			return
		}
		log.Infof("[Eino-LLM][OpenAI-Raw][Response] provider=%s model=%s method=%s url=%s status=%d stage=%s raw=%s",
			r.provider, r.model, r.method, r.url, r.statusCode, stage, r.body.String())
	})
}

func extractNonEmptyReasoningContent(chunk string) (string, bool) {
	searchFrom := 0
	for {
		idx := strings.Index(chunk[searchFrom:], reasoningContentMarker)
		if idx < 0 {
			return "", false
		}
		idx += searchFrom + len(reasoningContentMarker)

		pos := skipJSONWhitespace(chunk, idx)
		if pos >= len(chunk) || chunk[pos] != ':' {
			return "", false
		}

		pos = skipJSONWhitespace(chunk, pos+1)
		if pos >= len(chunk) {
			return "", false
		}

		if chunk[pos] != '"' {
			searchFrom = pos
			continue
		}

		content, complete := parseJSONStringValue(chunk, pos)
		if !complete {
			return "", false
		}
		if strings.TrimSpace(content) != "" {
			return content, true
		}
		searchFrom = pos + 1
	}
}

func skipJSONWhitespace(s string, pos int) int {
	for pos < len(s) {
		switch s[pos] {
		case ' ', '\n', '\r', '\t':
			pos++
		default:
			return pos
		}
	}
	return pos
}

func parseJSONStringValue(s string, start int) (string, bool) {
	if start >= len(s) || s[start] != '"' {
		return "", false
	}

	escaped := false
	for i := start + 1; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return s[start+1 : i], true
		}
	}
	return "", false
}

func (t *thinkingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.Body == nil || !t.needsPayloadRewrite() {
		if req != nil && req.Body == nil {
			t.logRequest(req, nil)
		}
		return t.roundTripAndWrap(req)
	}

	if req.Method != http.MethodPost {
		t.logRequest(req, nil)
		return t.roundTripAndWrap(req)
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()

	if len(bytes.TrimSpace(bodyBytes)) == 0 {
		clonedReq := cloneRequestWithBody(req, bodyBytes)
		t.logRequest(clonedReq, bodyBytes)
		return t.roundTripAndWrap(clonedReq)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		clonedReq := cloneRequestWithBody(req, bodyBytes)
		t.logRequest(clonedReq, bodyBytes)
		return t.roundTripAndWrap(clonedReq)
	}

	rewritten := false
	if shouldUseMaxCompletionTokens(t.provider, resolvePayloadModel(payload, t.model)) {
		rewritten = rewriteMaxTokensPayload(payload) || rewritten
	}

	if injectThinkingPayload(payload, t.provider, t.thinking) {
		rewritten = true
	}

	if !rewritten {
		clonedReq := cloneRequestWithBody(req, bodyBytes)
		t.logRequest(clonedReq, bodyBytes)
		return t.roundTripAndWrap(clonedReq)
	}

	newBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	clonedReq := cloneRequestWithBody(req, newBody)
	t.logRequest(clonedReq, newBody)
	return t.roundTripAndWrap(clonedReq)
}

func (t *thinkingRoundTripper) needsPayloadRewrite() bool {
	return t != nil
}

func (t *thinkingRoundTripper) roundTripAndWrap(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		log.Warnf("[Eino-LLM][OpenAI-Raw][Response] provider=%s model=%s method=%s url=%s status=0 stage=transport_error err=%v raw=",
			t.provider, t.model, requestMethod(req), requestURL(req), err)
		return resp, err
	}
	if resp == nil || resp.Body == nil {
		return resp, err
	}

	var wrappedBody io.ReadCloser = resp.Body
	if t.tracker != nil {
		wrappedBody = &reasoningDetectReadCloser{
			ReadCloser: wrappedBody,
			tracker:    t.tracker,
		}
	}
	resp.Body = &loggingResponseReadCloser{
		ReadCloser: wrappedBody,
		provider:   t.provider,
		model:      t.model,
		method:     requestMethod(req),
		url:        requestURL(req),
		statusCode: resp.StatusCode,
	}
	return resp, nil
}

func (t *thinkingRoundTripper) logRequest(req *http.Request, body []byte) {
	if req == nil {
		return
	}
	log.Infof("[Eino-LLM][OpenAI-Raw][Request] provider=%s model=%s method=%s url=%s raw=%s",
		t.provider, t.model, requestMethod(req), requestURL(req), string(body))
}

func requestMethod(req *http.Request) string {
	if req == nil {
		return ""
	}
	return req.Method
}

func requestURL(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	return req.URL.String()
}

func cloneRequestWithBody(req *http.Request, body []byte) *http.Request {
	cloned := req.Clone(req.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	cloned.Header = req.Header.Clone()
	if len(body) > 0 {
		cloned.Header.Set("Content-Length", strconv.Itoa(len(body)))
	} else {
		cloned.Header.Del("Content-Length")
	}
	return cloned
}

func parseThinkingConfig(config map[string]interface{}) thinkingConfig {
	parsed, err := decodeOpenAICompatibleConfig(config)
	if err != nil || parsed.Thinking == nil {
		return thinkingConfig{}
	}

	return *parsed.Thinking
}

func buildThinkingHTTPClient(config map[string]interface{}, base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	}

	thinking := parseThinkingConfig(config)

	parsed, err := decodeOpenAICompatibleConfig(config)
	if err != nil {
		return base
	}

	provider := parsed.Provider
	if provider == "" {
		provider = "openai"
	}

	var tracker *reasoningContentTracker
	if rawTracker, ok := config[reasoningTrackerConfigKey].(*reasoningContentTracker); ok {
		tracker = rawTracker
	}

	cloned := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cloned.Transport = &thinkingRoundTripper{
		base:     transport,
		provider: provider,
		model:    parsed.ModelName,
		thinking: thinking,
		tracker:  tracker,
	}
	return &cloned
}

func resolvePayloadModel(payload map[string]interface{}, fallback string) string {
	if payload != nil {
		if modelName, ok := payload["model"].(string); ok {
			modelName = strings.TrimSpace(modelName)
			if modelName != "" {
				return modelName
			}
		}
	}
	return strings.TrimSpace(fallback)
}

func shouldUseMaxCompletionTokens(provider, modelName string) bool {
	if !isOneOf(provider, "openai", "azure") {
		return false
	}

	modelName = strings.ToLower(strings.TrimSpace(modelName))
	return strings.HasPrefix(modelName, "o1") ||
		strings.HasPrefix(modelName, "o3") ||
		strings.HasPrefix(modelName, "o4")
}

func rewriteMaxTokensPayload(payload map[string]interface{}) bool {
	if payload == nil {
		return false
	}

	if _, exists := payload["max_completion_tokens"]; exists {
		delete(payload, "max_tokens")
		return true
	}

	maxTokens, exists := payload["max_tokens"]
	if !exists {
		return false
	}

	payload["max_completion_tokens"] = maxTokens
	delete(payload, "max_tokens")
	return true
}

func injectThinkingPayload(payload map[string]interface{}, provider string, thinking thinkingConfig) bool {
	switch provider {
	case "openai", "azure":
		if isOneOf(thinking.Mode, "none", "minimal", "low", "medium", "high", "xhigh") {
			payload["reasoning_effort"] = thinking.Mode
			return true
		}
	case "anthropic":
		if thinking.Mode == "enabled" {
			if thinking.BudgetTokens == nil || *thinking.BudgetTokens <= 0 {
				return false
			}
			payload["thinking"] = map[string]interface{}{
				"type":          "enabled",
				"budget_tokens": *thinking.BudgetTokens,
			}
			return true
		}
		if thinking.Mode == "adaptive" {
			payload["thinking"] = map[string]interface{}{
				"type": "adaptive",
			}
			payload["output_config"] = map[string]interface{}{
				"effort": normalizeThinkingEffort(thinking.Effort),
			}
			return true
		}
	case "doubao":
		if isOneOf(thinking.Mode, "minimal", "low", "medium", "high") {
			payload["reasoning_effort"] = thinking.Mode
			return true
		}
	case "zhipu", "deepseek":
		if isOneOf(thinking.Mode, "enabled", "disabled") {
			thinkingPayload := map[string]interface{}{
				"type": thinking.Mode,
			}
			if provider == "zhipu" && thinking.ClearThinking != nil {
				thinkingPayload["clear_thinking"] = *thinking.ClearThinking
			}
			payload["thinking"] = thinkingPayload
			return true
		}
	case "aliyun", "siliconflow":
		if thinking.Mode == "enabled" {
			payload["enable_thinking"] = true
			if thinking.BudgetTokens != nil && *thinking.BudgetTokens > 0 {
				payload["thinking_budget"] = *thinking.BudgetTokens
			}
			return true
		}
		if thinking.Mode == "disabled" {
			payload["enable_thinking"] = false
			delete(payload, "thinking_budget")
			return true
		}
	}
	return false
}

func isOneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func normalizeThinkingEffort(effort string) string {
	if isOneOf(effort, "low", "medium", "high", "max") {
		return effort
	}
	return defaultThinkingEffort
}
