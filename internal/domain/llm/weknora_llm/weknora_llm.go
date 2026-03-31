package weknora_llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	llm_common "xiaozhi-esp32-server-golang/internal/domain/llm/common"
	log "xiaozhi-esp32-server-golang/logger"

	"github.com/cloudwego/eino/schema"
	sse "github.com/tmaxmax/go-sse"
)

const (
	defaultWeknoraBaseURL = "http://127.0.0.1:8080"
	defaultUserPrefix     = "xiaozhi"
	llmExtraErrorKey      = "error"

	sessionCreatePath = "/api/v1/sessions"
	agentChatPath     = "/api/v1/agent-chat/"
	sessionStopPath   = "/api/v1/sessions/%s/stop"

	maxIdleConns        = 200
	maxIdleConnsPerHost = 50
	idleConnTimeout     = 90 * time.Second
	dialTimeout         = 30 * time.Second
	keepAliveTimeout    = 30 * time.Second
)

var (
	httpClientOnce sync.Once
	httpClientInst *http.Client
)

type WeknoraLLMProvider struct {
	apiKey           string
	baseURL          string
	userPrefix       string
	agentID          string
	agentEnabled     bool
	webSearchEnabled bool
	httpClient       *http.Client

	sessionMu  sync.RWMutex
	sessionIDs map[string]string // xiaozhi sessionID -> weknora session UUID
}

type weknoraCreateSessionRequest struct {
	Title string `json:"title,omitempty"`
}

type weknoraCreateSessionResponse struct {
	Data    weknoraSessionData `json:"data"`
	Success bool               `json:"success"`
}

type weknoraSessionData struct {
	ID string `json:"id"`
}

type weknoraAgentChatRequest struct {
	Query            string `json:"query"`
	AgentEnabled     bool   `json:"agent_enabled"`
	WebSearchEnabled bool   `json:"web_search_enabled,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
}

type weknoraStreamEvent struct {
	ID                  string      `json:"id"`
	ResponseType        string      `json:"response_type"`
	Content             string      `json:"content"`
	Done                bool        `json:"done"`
	KnowledgeReferences interface{} `json:"knowledge_references"`
	Data                interface{} `json:"data,omitempty"`
}

type weknoraStopRequest struct {
	MessageID string `json:"message_id"`
}

func getHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: keepAliveTimeout,
			}).DialContext,
			MaxIdleConns:        maxIdleConns,
			MaxIdleConnsPerHost: maxIdleConnsPerHost,
			IdleConnTimeout:     idleConnTimeout,
			DisableKeepAlives:   false,
		}
		httpClientInst = &http.Client{
			Transport: transport,
			Timeout:   0,
		}
	})
	return httpClientInst
}

func NewWeknoraLLMProvider(config map[string]interface{}) (*WeknoraLLMProvider, error) {
	apiKey, _ := config["api_key"].(string)
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("weknora api_key不能为空")
	}

	baseURL, _ := config["base_url"].(string)
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultWeknoraBaseURL
	}
	rawURL := baseURL
	baseURL = strings.TrimRight(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/api/v1")
	if rawURL != baseURL {
		log.Infof("weknora base_url已自动规范化: %q -> %q", rawURL, baseURL)
	}

	userPrefix, _ := config["user_prefix"].(string)
	userPrefix = strings.TrimSpace(userPrefix)
	if userPrefix == "" {
		userPrefix = defaultUserPrefix
	}

	agentID, _ := config["agent_id"].(string)
	agentID = strings.TrimSpace(agentID)

	agentEnabled := true
	if v, ok := config["agent_enabled"]; ok {
		if b, ok := v.(bool); ok {
			agentEnabled = b
		}
	}

	webSearchEnabled := false
	if v, ok := config["web_search_enabled"]; ok {
		if b, ok := v.(bool); ok {
			webSearchEnabled = b
		}
	}

	return &WeknoraLLMProvider{
		apiKey:           apiKey,
		baseURL:          baseURL,
		userPrefix:       userPrefix,
		agentID:          agentID,
		agentEnabled:     agentEnabled,
		webSearchEnabled: webSearchEnabled,
		httpClient:       getHTTPClient(),
		sessionIDs:       make(map[string]string),
	}, nil
}

func (p *WeknoraLLMProvider) ResponseWithContext(ctx context.Context, sessionID string, dialogue []*schema.Message, _ []*schema.ToolInfo) chan *schema.Message {
	out := make(chan *schema.Message, 200)

	go func() {
		defer close(out)

		query := buildWeknoraQuery(dialogue)
		if strings.TrimSpace(query) == "" {
			sendLLMError(out, fmt.Errorf("weknora query不能为空"))
			return
		}

		weknoraSessionID, err := p.getOrCreateSession(ctx, sessionID)
		if err != nil {
			sendLLMError(out, fmt.Errorf("weknora创建会话失败: %w", err))
			return
		}

		reqBody := weknoraAgentChatRequest{
			Query:            query,
			AgentEnabled:     p.agentEnabled,
			WebSearchEnabled: p.webSearchEnabled,
		}
		if p.agentID != "" {
			reqBody.AgentID = p.agentID
		}

		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			sendLLMError(out, err)
			return
		}

		url := p.baseURL + agentChatPath + weknoraSessionID
		log.Debugf("weknora agent-chat request: url=%s body=%s", url, string(bodyBytes))

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			sendLLMError(out, err)
			return
		}
		req.Header.Set("X-API-Key", p.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")

		resp, err := p.httpClient.Do(req)
		if err != nil {
			sendLLMError(out, fmt.Errorf("weknora请求失败: %w", err))
			return
		}
		defer resp.Body.Close()
		log.Debugf("weknora agent-chat response: status=%d", resp.StatusCode)

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			log.Warnf("weknora agent-chat error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(errBody)))
			sendLLMError(out, fmt.Errorf("weknora请求失败 status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(errBody))))
			return
		}

		var lastMessageID string
		var eventCount int
		for event, eventErr := range sse.Read(resp.Body, nil) {
			eventCount++
			if eventErr != nil {
				if ctx.Err() != nil {
					break
				}
				sendLLMError(out, fmt.Errorf("weknora流读取失败: %w", eventErr))
				return
			}

			data := strings.TrimSpace(event.Data)
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				return
			}

			var streamEvent weknoraStreamEvent
			if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
				log.Warnf("解析weknora流事件失败: %v, data=%s", err, previewString(data, 256))
				continue
			}

			if streamEvent.ID != "" {
				lastMessageID = streamEvent.ID
			}

			if streamEvent.ResponseType != "answer" {
				log.Debugf("weknora stream event #%d: type=%s done=%v content_len=%d", eventCount, streamEvent.ResponseType, streamEvent.Done, len(streamEvent.Content))
			}

			switch streamEvent.ResponseType {
			case "error":
				msg := streamEvent.Content
				if msg == "" {
					msg = "weknora返回错误"
				}
				sendLLMError(out, fmt.Errorf("%s", msg))
				return
			case "answer":
				content := stripThinkTags(streamEvent.Content)
				if content != "" {
					out <- &schema.Message{
						Role:    schema.Assistant,
						Content: content,
					}
				}
				if shouldTerminateStreamEvent(streamEvent) {
					log.Debugf("weknora stream completed after %d events", eventCount)
					return
				}
			case "complete":
				log.Debugf("weknora stream completed (explicit) after %d events", eventCount)
				return
			default:
				if streamEvent.Done {
					log.Debugf("weknora stream auxiliary event type=%s done=%v ignored after %d events", streamEvent.ResponseType, streamEvent.Done, eventCount)
				}
			}
		}

		if ctx.Err() != nil && lastMessageID != "" {
			p.stopSession(weknoraSessionID, lastMessageID)
		}
	}()

	return out
}

func (p *WeknoraLLMProvider) getOrCreateSession(ctx context.Context, sessionID string) (string, error) {
	stableKey := llm_common.BuildStableUserID(p.userPrefix, sessionID)

	p.sessionMu.RLock()
	if wkID, ok := p.sessionIDs[stableKey]; ok {
		p.sessionMu.RUnlock()
		return wkID, nil
	}
	p.sessionMu.RUnlock()

	reqBody := weknoraCreateSessionRequest{}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := p.baseURL + sessionCreatePath
	log.Debugf("weknora create-session request: url=%s body=%s", url, string(bodyBytes))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("创建weknora session失败: %w", err)
	}
	defer resp.Body.Close()
	log.Debugf("weknora create-session response: status=%d", resp.StatusCode)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("创建weknora session失败 status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var sessionResp weknoraCreateSessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		return "", fmt.Errorf("解析weknora session响应失败: %w", err)
	}

	wkSessionID := strings.TrimSpace(sessionResp.Data.ID)
	if wkSessionID == "" {
		return "", fmt.Errorf("weknora返回的session id为空")
	}

	p.sessionMu.Lock()
	p.sessionIDs[stableKey] = wkSessionID
	p.sessionMu.Unlock()

	log.Infof("weknora session created: xiaozhi_key=%s weknora_session=%s", stableKey, wkSessionID)
	return wkSessionID, nil
}

func (p *WeknoraLLMProvider) stopSession(weknoraSessionID, messageID string) {
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	bodyBytes, err := json.Marshal(weknoraStopRequest{MessageID: messageID})
	if err != nil {
		return
	}

	url := fmt.Sprintf(p.baseURL+sessionStopPath, weknoraSessionID)
	req, err := http.NewRequestWithContext(stopCtx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return
	}
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		log.Debugf("weknora stop session请求失败: %v", err)
		return
	}
	defer resp.Body.Close()
}

func buildWeknoraQuery(dialogue []*schema.Message) string {
	if len(dialogue) == 0 {
		return ""
	}

	for i := len(dialogue) - 1; i >= 0; i-- {
		msg := dialogue[i]
		if msg == nil || msg.Role != schema.User {
			continue
		}
		if text := extractMessageText(msg); text != "" {
			return text
		}
	}

	for i := len(dialogue) - 1; i >= 0; i-- {
		if text := extractMessageText(dialogue[i]); text != "" {
			return text
		}
	}

	return ""
}

func extractMessageText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	if text := strings.TrimSpace(msg.Content); text != "" {
		return text
	}
	if len(msg.MultiContent) > 0 {
		parts := make([]string, 0, len(msg.MultiContent))
		for _, part := range msg.MultiContent {
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

func (p *WeknoraLLMProvider) ResponseWithVllm(_ context.Context, _ []byte, _ string, _ string) (string, error) {
	return "", fmt.Errorf("weknora provider不支持vllm能力")
}

func (p *WeknoraLLMProvider) GetModelInfo() map[string]interface{} {
	info := map[string]interface{}{
		"type":     "weknora",
		"provider": "weknora",
		"base_url": p.baseURL,
	}
	if p.agentID != "" {
		info["agent_id"] = p.agentID
	}
	return info
}

func (p *WeknoraLLMProvider) Close() error {
	return nil
}

func (p *WeknoraLLMProvider) IsValid() bool {
	return p != nil && p.apiKey != "" && p.baseURL != ""
}

var thinkTagRegexp = regexp.MustCompile(`(?s)<think>.*?</think>`)

func stripThinkTags(text string) string {
	result := thinkTagRegexp.ReplaceAllString(text, "")
	if strings.TrimSpace(result) == "" {
		return ""
	}
	return strings.Trim(result, "\r\n\t")
}

func shouldTerminateStreamEvent(event weknoraStreamEvent) bool {
	switch event.ResponseType {
	case "answer":
		return event.Done
	case "complete":
		return true
	default:
		return false
	}
}

func sendLLMError(ch chan *schema.Message, err error) {
	ch <- &schema.Message{
		Role:  schema.System,
		Extra: map[string]any{llmExtraErrorKey: err.Error()},
	}
}

func previewString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
