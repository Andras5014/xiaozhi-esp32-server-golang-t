package eino_llm

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	log "xiaozhi-esp32-server-golang/logger"
)

const (
	defaultThinkingEffort     = "medium"
	reasoningContentMarker    = "\"reasoning_content\""
	reasoningTrackerConfigKey = "__reasoning_content_tracker"
	reasoningDetectConfigKey  = "__enable_reasoning_content_detection"
	reasoningDetectTailSize   = 1024
	compatDebugPayloadLimit   = 24 * 1024
	compatDebugTextLimit      = 512
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
	baseURL  string
	model    string
	thinking thinkingConfig
	tracker  *reasoningContentTracker
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
		return t.roundTripAndWrap(req, nil, nil)
	}

	if req.Method != http.MethodPost {
		return t.roundTripAndWrap(req, nil, nil)
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()

	if len(bytes.TrimSpace(bodyBytes)) == 0 {
		clonedReq := cloneRequestWithBody(req, bodyBytes)
		return t.roundTripAndWrap(clonedReq, nil, bodyBytes)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		clonedReq := cloneRequestWithBody(req, bodyBytes)
		return t.roundTripAndWrap(clonedReq, nil, bodyBytes)
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
		return t.roundTripAndWrap(clonedReq, payload, bodyBytes)
	}

	newBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	clonedReq := cloneRequestWithBody(req, newBody)
	return t.roundTripAndWrap(clonedReq, payload, newBody)
}

func (t *thinkingRoundTripper) needsPayloadRewrite() bool {
	return t != nil
}

func (t *thinkingRoundTripper) roundTripAndWrap(req *http.Request, payload map[string]interface{}, body []byte) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.logCompatFailure(req, payload, body, 0, "", err)
		return resp, err
	}
	if resp != nil && resp.Body != nil && shouldLogOpenAICompatDebug(t.provider, requestURL(req)) && resp.StatusCode >= http.StatusBadRequest {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		if readErr != nil {
			t.logCompatFailure(req, payload, body, resp.StatusCode, "", readErr)
		} else {
			t.logCompatFailure(req, payload, body, resp.StatusCode, truncateForCompatDebug(bodyBytes, compatDebugPayloadLimit), nil)
		}
	}
	if resp == nil || resp.Body == nil || t.tracker == nil {
		return resp, err
	}
	resp.Body = &reasoningDetectReadCloser{
		ReadCloser: resp.Body,
		tracker:    t.tracker,
	}
	return resp, nil
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
		baseURL:  parsed.BaseURL,
		model:    parsed.ModelName,
		thinking: thinking,
		tracker:  tracker,
	}
	return &cloned
}

func shouldLogOpenAICompatDebug(provider, endpoint string) bool {
	return true
}

func (t *thinkingRoundTripper) logCompatRequest(req *http.Request, payload map[string]interface{}, body []byte) {
	if !shouldLogOpenAICompatDebug(t.provider, requestURL(req)) {
		return
	}
	summary := summarizeCompatPayload(payload)
	log.Infof("[Eino-LLM][Compat-Debug] provider=%s method=%s url=%s summary=%s raw=%s",
		t.provider,
		requestMethod(req),
		requestURL(req),
		marshalCompatDebugValue(summary),
		truncateForCompatDebug(body, compatDebugPayloadLimit),
	)
}

func (t *thinkingRoundTripper) logCompatFailure(req *http.Request, payload map[string]interface{}, body []byte, statusCode int, responseBody string, err error) {
	if !shouldLogOpenAICompatDebug(t.provider, requestURL(req)) {
		return
	}

	summary := summarizeCompatPayload(payload)
	if err != nil {
		log.Warnf("[Eino-LLM][Compat-Debug] provider=%s method=%s url=%s status=%d err=%v summary=%s raw=%s response=%s",
			t.provider,
			requestMethod(req),
			requestURL(req),
			statusCode,
			err,
			marshalCompatDebugValue(summary),
			truncateForCompatDebug(body, compatDebugPayloadLimit),
			responseBody,
		)
		return
	}

	log.Warnf("[Eino-LLM][Compat-Debug] provider=%s method=%s url=%s status=%d summary=%s raw=%s response=%s",
		t.provider,
		requestMethod(req),
		requestURL(req),
		statusCode,
		marshalCompatDebugValue(summary),
		truncateForCompatDebug(body, compatDebugPayloadLimit),
		responseBody,
	)
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

func summarizeCompatPayload(payload map[string]interface{}) map[string]interface{} {
	if len(payload) == 0 {
		return map[string]interface{}{}
	}

	summary := map[string]interface{}{
		"model":        payload["model"],
		"stream":       payload["stream"],
		"tool_choice":  payload["tool_choice"],
		"messages":     summarizeCompatMessages(payload["messages"]),
		"tools_count":  summarizeCompatToolsCount(payload["tools"]),
		"tool_names":   summarizeCompatToolNames(payload["tools"]),
		"max_tokens":   firstCompatValue(payload, "max_tokens", "max_completion_tokens"),
		"temperature":  payload["temperature"],
		"top_p":        payload["top_p"],
		"response_fmt": payload["response_format"],
	}

	return summary
}

func summarizeCompatMessages(raw interface{}) []map[string]interface{} {
	items, ok := raw.([]interface{})
	if !ok || len(items) == 0 {
		return nil
	}

	summary := make([]map[string]interface{}, 0, len(items))
	for idx, item := range items {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		entry := map[string]interface{}{
			"idx":            idx,
			"role":           msg["role"],
			"name":           msg["name"],
			"content_exists": compatMapHasKey(msg, "content"),
			"content":        summarizeCompatContent(msg["content"]),
			"tool_call_id":   msg["tool_call_id"],
			"tool_calls":     summarizeCompatToolCalls(msg["tool_calls"]),
		}

		if value, ok := msg["content"]; ok && value == nil {
			entry["content_is_null"] = true
		}

		summary = append(summary, entry)
	}

	return summary
}

func summarizeCompatContent(raw interface{}) interface{} {
	switch typed := raw.(type) {
	case string:
		return truncateStringForCompatDebug(typed, compatDebugTextLimit)
	case []interface{}:
		parts := make([]map[string]interface{}, 0, len(typed))
		for _, part := range typed {
			partMap, ok := part.(map[string]interface{})
			if !ok {
				continue
			}
			entry := map[string]interface{}{
				"type": partMap["type"],
			}
			if text, ok := partMap["text"].(string); ok {
				entry["text"] = truncateStringForCompatDebug(text, compatDebugTextLimit)
			}
			parts = append(parts, entry)
		}
		return parts
	default:
		return raw
	}
}

func summarizeCompatToolCalls(raw interface{}) []map[string]interface{} {
	items, ok := raw.([]interface{})
	if !ok || len(items) == 0 {
		return nil
	}

	summary := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		tc, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		entry := map[string]interface{}{
			"id":   tc["id"],
			"type": tc["type"],
		}
		if fn, ok := tc["function"].(map[string]interface{}); ok {
			entry["function"] = map[string]interface{}{
				"name":      fn["name"],
				"arguments": truncateStringForCompatDebug(asCompatString(fn["arguments"]), compatDebugTextLimit),
			}
		}
		summary = append(summary, entry)
	}
	return summary
}

func summarizeCompatToolsCount(raw interface{}) int {
	items, ok := raw.([]interface{})
	if !ok {
		return 0
	}
	return len(items)
}

func summarizeCompatToolNames(raw interface{}) []string {
	items, ok := raw.([]interface{})
	if !ok || len(items) == 0 {
		return nil
	}

	names := make([]string, 0, len(items))
	for _, item := range items {
		toolMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if fn, ok := toolMap["function"].(map[string]interface{}); ok {
			name := strings.TrimSpace(asCompatString(fn["name"]))
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func firstCompatValue(payload map[string]interface{}, keys ...string) interface{} {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			return value
		}
	}
	return nil
}

func compatMapHasKey(input map[string]interface{}, key string) bool {
	if input == nil {
		return false
	}
	_, ok := input[key]
	return ok
}

func asCompatString(v interface{}) string {
	if str, ok := v.(string); ok {
		return str
	}
	if v == nil {
		return ""
	}
	return marshalCompatDebugValue(v)
}

func marshalCompatDebugValue(v interface{}) string {
	if v == nil {
		return "null"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return strconv.Quote(truncateStringForCompatDebug(asCompatStringFallback(v), compatDebugTextLimit))
	}
	return string(data)
}

func asCompatStringFallback(v interface{}) string {
	switch typed := v.(type) {
	case string:
		return typed
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(marshalCompatDebugValueWithoutJSON(v)), "\r", " "), "\n", " "))
	}
}

func marshalCompatDebugValueWithoutJSON(v interface{}) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(marshalCompatDebugValueSafe(v)), `"`), `"`)), "\r", " "), "\n", " "))
}

func marshalCompatDebugValueSafe(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func truncateForCompatDebug(body []byte, limit int) string {
	text := string(body)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "...(truncated)"
}

func truncateStringForCompatDebug(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
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
