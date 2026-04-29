package eino_llm

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	log "xiaozhi-esp32-server-golang/logger"
)

// EinoLLMProvider 基于Eino框架的LLM提供者
// 直接使用Eino的ChatModel接口和类型，支持openai和ollama
type EinoLLMProvider struct {
	chatModel        model.ToolCallingChatModel
	modelName        string
	maxTokens        int
	streamable       bool
	config           map[string]interface{}
	providerType     string // "openai" 或 "ollama"
	reasoningTracker *reasoningContentTracker
	reasoningStore   *reasoningSessionStore
}

// EinoConfig Eino LLM配置
type EinoConfig struct {
	Type       string                 `json:"type"` // "openai" 或 "ollama"
	ModelName  string                 `json:"model_name"`
	APIKey     string                 `json:"api_key"`
	BaseURL    string                 `json:"base_url"`
	MaxTokens  int                    `json:"max_tokens"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
	Streamable bool                   `json:"streamable,omitempty"`
}

// 连接池配置
const (
	maxIdleConns          = 200
	maxIdleConnsPerHost   = 50
	idleConnTimeout       = 90 * time.Second
	dialTimeout           = 30 * time.Second
	keepAliveTimeout      = 30 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 60 * time.Second
)

// 全局HTTP客户端，用于所有OpenAI请求
var (
	httpClient     *http.Client
	httpClientOnce sync.Once
)

// getHTTPClient 返回配置了连接池的HTTP客户端
func getHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: keepAliveTimeout,
			}).DialContext,
			MaxIdleConns:          maxIdleConns,
			MaxIdleConnsPerHost:   maxIdleConnsPerHost,
			IdleConnTimeout:       idleConnTimeout,
			TLSHandshakeTimeout:   tlsHandshakeTimeout,
			ResponseHeaderTimeout: responseHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     false,
		}

		httpClient = &http.Client{
			Transport: transport,
			// 流式输出场景不要用 http.Client.Timeout 截断整个连接，改由 ctx 控制请求生命周期。
			Timeout: 0,
		}
	})

	return httpClient
}

// NewEinoLLMProvider 创建新的Eino LLM提供者，根据type支持openai和ollama
func NewEinoLLMProvider(config map[string]interface{}) (*EinoLLMProvider, error) {
	//log.Debugf("NewEinoLLMProvider config: %+v", config)
	var tracker *reasoningContentTracker
	var store *reasoningSessionStore
	if enabled, _ := config[reasoningDetectConfigKey].(bool); enabled {
		tracker = &reasoningContentTracker{}
		store = newReasoningSessionStore()
		config[reasoningTrackerConfigKey] = tracker
		config[reasoningStoreConfigKey] = store
	}
	parsedConfig, err := decodeOpenAICompatibleConfig(config)
	if err != nil {
		return nil, fmt.Errorf("解析LLM配置失败: %v", err)
	}

	providerType := parsedConfig.Type
	if providerType == "" {
		return nil, fmt.Errorf("type不能为空，必须是 'openai' 或 'ollama'")
	}

	modelName := parsedConfig.ModelName
	if modelName == "" {
		return nil, fmt.Errorf("model_name不能为空")
	}

	maxTokens := 500
	if parsedConfig.MaxTokens != nil {
		maxTokens = *parsedConfig.MaxTokens
	}

	streamable := true
	if parsedConfig.Streamable != nil {
		streamable = *parsedConfig.Streamable
	}

	var chatModel model.ToolCallingChatModel

	// 根据类型创建不同的ChatModel实现
	switch providerType {
	case "openai":
		chatModel, err = createOpenAIChatModel(config)
		if err != nil {
			return nil, fmt.Errorf("创建OpenAI ChatModel失败: %v", err)
		}
	case "ollama":
		chatModel, err = createOllamaChatModel(config)
		if err != nil {
			return nil, fmt.Errorf("创建Ollama ChatModel失败: %v", err)
		}
	default:
		return nil, fmt.Errorf("不支持的模型类型: %s", providerType)
	}

	provider := &EinoLLMProvider{
		chatModel:        chatModel,
		modelName:        modelName,
		maxTokens:        maxTokens,
		streamable:       streamable,
		config:           config,
		providerType:     providerType,
		reasoningTracker: tracker,
		reasoningStore:   store,
	}

	return provider, nil
}

func (p *EinoLLMProvider) HasReasoningContent() bool {
	return p != nil && p.reasoningTracker != nil && p.reasoningTracker.HasReturned()
}

// createOpenAIChatModel 创建OpenAI的ChatModel实现
func createOpenAIChatModel(config map[string]interface{}) (model.ToolCallingChatModel, error) {
	ctx := context.Background()

	parsedConfig, err := decodeOpenAICompatibleConfig(config)
	if err != nil {
		return nil, fmt.Errorf("解析OpenAI兼容配置失败: %v", err)
	}

	modelName := parsedConfig.ModelName
	if modelName == "" {
		modelName = "gpt-3.5-turbo"
	}

	apiKey := parsedConfig.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	httpClient := buildThinkingHTTPClient(config, getHTTPClient())
	useMaxCompletionTokens := shouldUseMaxCompletionTokens(parsedConfig.Provider, modelName)

	// 创建OpenAI ChatModel配置
	openaiConfig := &openai.ChatModelConfig{
		Model:      modelName,
		APIKey:     apiKey,
		HTTPClient: httpClient,
	}

	if parsedConfig.BaseURL != "" {
		openaiConfig.BaseURL = parsedConfig.BaseURL
	}
	if parsedConfig.APIVersion != "" {
		openaiConfig.APIVersion = parsedConfig.APIVersion
	}
	if !useMaxCompletionTokens && parsedConfig.MaxTokens != nil && *parsedConfig.MaxTokens > 0 {
		openaiConfig.MaxTokens = parsedConfig.MaxTokens
	}
	if parsedConfig.Temperature != nil {
		openaiConfig.Temperature = parsedConfig.Temperature
	}
	if parsedConfig.TopP != nil {
		openaiConfig.TopP = parsedConfig.TopP
	}

	log.Debugf("openaiConfig: %+v", openaiConfig)

	// 使用eino-ext官方OpenAI实现
	chatModel, err := openai.NewChatModel(ctx, openaiConfig)
	if err != nil {
		return nil, fmt.Errorf("创建OpenAI ChatModel失败: %v", err)
	}

	log.Infof("成功创建OpenAI ChatModel，模型: %s", modelName)
	return chatModel, nil
}

// createOllamaChatModel 创建Ollama的ChatModel实现
func createOllamaChatModel(config map[string]interface{}) (model.ToolCallingChatModel, error) {
	ctx := context.Background()

	modelName, _ := config["model_name"].(string)
	baseURL, _ := config["base_url"].(string)

	if modelName == "" || baseURL == "" {
		log.Warnf("model_name和base_url不能为空，使用默认模型: %s", modelName)
		return nil, fmt.Errorf("model_name和base_url不能为空")
	}

	// 创建Ollama ChatModel配置
	ollamaConfig := &ollama.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
	}

	// 使用eino-ext官方Ollama实现
	chatModel, err := ollama.NewChatModel(ctx, ollamaConfig)
	if err != nil {
		return nil, fmt.Errorf("创建Ollama ChatModel失败: %v", err)
	}

	log.Infof("成功创建Ollama ChatModel，模型: %s", modelName)
	return chatModel, nil
}

// GetModelInfo 获取模型信息
func (p *EinoLLMProvider) GetModelInfo() map[string]interface{} {
	return map[string]interface{}{
		"model_name":      p.modelName,
		"max_tokens":      p.maxTokens,
		"streamable":      p.streamable,
		"type":            "eino",
		"provider_type":   p.providerType,
		"framework":       "eino",
		"adapter_version": "3.0.0",
		"base_url":        p.config["base_url"],
	}
}

// ResponseWithFunctions 带函数调用的响应，使用Eino原生工具类型，直接调用EinoResponseWithTools
func (p *EinoLLMProvider) ResponseWithContext(ctx context.Context, sessionID string, dialogue []*schema.Message, functions []*schema.ToolInfo) chan *schema.Message {

	log.Infof("[Eino-LLM] 开始处理带工具的请求 - SessionID: %s, Type: %s", sessionID, p.providerType)

	logMessages(dialogue)
	// 直接调用EinoResponseWithTools获取Eino原生响应
	einoResponseChan := p.EinoResponseWithTools(ctx, sessionID, dialogue, functions)

	log.Infof("[Eino-LLM] 工具调用请求处理完成 - SessionID: %s", sessionID)

	return einoResponseChan
}

func logMessages(messages []*schema.Message) {
	for _, msg := range messages {
		if msg == nil {
			log.Debugf("history llm msg: <nil>")
			continue
		}
		log.Debugf("history llm msg: %s\n", msg.String())
	}
}

// llmExtraErrorKey 与 domain/llm.LLMExtraErrorKey 保持一致，失败时透传错误用（避免循环依赖）
const llmExtraErrorKey = "error"

// sendLLMError 向 channel 发送带 Extra.error 的错误消息
func sendLLMError(ch chan *schema.Message, err error) {
	ch <- &schema.Message{
		Role:  schema.System,
		Extra: map[string]any{llmExtraErrorKey: err.Error()},
	}
}

// EinoResponseWithTools 直接使用Eino类型的带工具响应
func (p *EinoLLMProvider) EinoResponseWithTools(ctx context.Context, sessionID string, messages []*schema.Message, tools []*schema.ToolInfo) chan *schema.Message {
	responseChan := make(chan *schema.Message, 200)

	go func() {
		defer close(responseChan)
		ctx = withReasoningSessionID(ctx, sessionID)
		if p.reasoningTracker != nil {
			p.reasoningTracker.Reset()
		}

		log.Infof("[Eino-LLM] 开始处理Eino工具请求 - SessionID: %s, tools: %+v", sessionID, tools)
		logToolRequestDiagnostics(sessionID, messages, nil)

		chatModel, toolNameAliases, err := p.requestChatModel(messages, tools)
		if err != nil {
			log.Errorf("准备ChatModel失败: %v", err)
			logToolRequestDiagnostics(sessionID, messages, err)
			sendLLMError(responseChan, err)
			return
		}

		if p.streamable {
			log.Debugf("EinoLLMProvider.EinoResponseWithTools() streamable: %t", p.streamable)
			// 直接使用Eino的Stream方法
			streamReader, err := chatModel.Stream(ctx, messages, p.buildModelCallOptions()...)
			if err != nil {
				log.Errorf("Eino工具流式调用失败: %v", err)
				logToolRequestDiagnostics(sessionID, messages, err)
				// 对于mock实现，如果Stream失败，回退到Generate
				message, genErr := chatModel.Generate(ctx, messages, p.buildModelCallOptions()...)
				if genErr != nil {
					log.Errorf("Eino工具生成响应失败: %v", genErr)
					logToolRequestDiagnostics(sessionID, messages, genErr)
					sendLLMError(responseChan, genErr)
					return
				}
				if message != nil {
					message = restoreOriginalToolCallNames(message, toolNameAliases)
					message = p.attachReasoningContent(message)
					p.storeReasoningContent(sessionID, message.ToolCalls)
					responseChan <- message
				}
				return
			}

			if streamReader != nil {
				defer streamReader.Close()

				var currentToolCall *schema.ToolCall
				completedToolCalls := make([]schema.ToolCall, 0, 1)
				var toolCallBuffer string
				var isToolCallComplete bool
				var streamChunkCount int

				// 处理流式响应
				for {
					message, err := streamReader.Recv()
					//log.Debugf("streamReader.Recv() message: %+v", message)
					if err == io.EOF {
						if streamChunkCount == 0 {
							sendLLMError(responseChan, errors.New("流式响应为空"))
							break
						}
						// 如果有未完成的工具调用，发送最后一次
						if currentToolCall != nil {
							completeMessage := &schema.Message{
								Role:      schema.Assistant,
								ToolCalls: []schema.ToolCall{*currentToolCall},
							}
							completeMessage = p.attachReasoningContent(completeMessage)
							responseChan <- completeMessage
							completedToolCalls = append(completedToolCalls, completeMessage.ToolCalls...)
						}
						p.storeReasoningContent(sessionID, completedToolCalls)
						break
					}
					if err != nil {
						if ctxErr := ctx.Err(); ctxErr != nil {
							if errors.Is(ctxErr, context.Canceled) {
								log.Debugf("流式响应已取消: %v", ctxErr)
							} else {
								log.Warnf("流式响应已结束: %v", ctxErr)
							}
							break
						}
						log.Errorf("接收流式响应失败: %v", err)
						sendLLMError(responseChan, err)
						break
					}

					if message != nil {
						message = restoreOriginalToolCallNames(message, toolNameAliases)
						streamChunkCount++
						// 检查是否是工具调用的开始
						if len(message.ToolCalls) > 0 {
							toolCall := message.ToolCalls[0]

							if toolCall.Function.Name != "" {
								// 新工具调用开始
								currentToolCall = &toolCall
								toolCallBuffer = toolCall.Function.Arguments
								isToolCallComplete = false
							} else if currentToolCall != nil {
								// 累积工具调用参数
								toolCallBuffer += toolCall.Function.Arguments
								currentToolCall.Function.Arguments = toolCallBuffer

								// 检查参数是否是完整的 JSON
								if isValidJSON(toolCallBuffer) {
									isToolCallComplete = true
								}
							}

							// 如果工具调用完整，发送消息
							if isToolCallComplete {
								completeMessage := &schema.Message{
									Role:      schema.Assistant,
									ToolCalls: []schema.ToolCall{*currentToolCall},
								}
								completeMessage = p.attachReasoningContent(completeMessage)
								responseChan <- completeMessage
								completedToolCalls = append(completedToolCalls, completeMessage.ToolCalls...)

								// 重置状态
								currentToolCall = nil
								toolCallBuffer = ""
								isToolCallComplete = false
							}
						} else if message.Content != "" {
							// 发送非工具调用的普通消息
							message.ToolCalls = nil
							message = p.attachReasoningContent(message)
							responseChan <- message
						}
					}
				}
			} else {
				sendLLMError(responseChan, errors.New("流式响应为空"))
			}
		} else {
			// 直接使用Eino的Generate方法
			message, err := chatModel.Generate(ctx, messages, p.buildModelCallOptions()...)
			if err != nil {
				log.Errorf("Eino工具生成响应失败: %v", err)
				logToolRequestDiagnostics(sessionID, messages, err)
				sendLLMError(responseChan, err)
				return
			}

			if message != nil {
				message = restoreOriginalToolCallNames(message, toolNameAliases)
				message = p.attachReasoningContent(message)
				p.storeReasoningContent(sessionID, message.ToolCalls)
				responseChan <- message
			}
		}

		log.Infof("[Eino-LLM] Eino工具请求处理完成 - SessionID: %s", sessionID)
	}()

	return responseChan
}

func (p *EinoLLMProvider) attachReasoningContent(message *schema.Message) *schema.Message {
	if message == nil || p == nil || p.reasoningTracker == nil {
		return message
	}

	content := strings.TrimSpace(p.reasoningTracker.Content())
	if content == "" {
		return message
	}

	if message.Extra == nil {
		message.Extra = make(map[string]any, 1)
	}
	message.Extra[reasoningContentExtraKey] = content
	return message
}

func (p *EinoLLMProvider) storeReasoningContent(sessionID string, toolCalls []schema.ToolCall) {
	if p == nil || p.reasoningStore == nil || p.reasoningTracker == nil {
		return
	}

	content := strings.TrimSpace(p.reasoningTracker.Content())
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || content == "" || len(toolCalls) == 0 {
		return
	}

	toolCallIDs := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		toolCallID := strings.TrimSpace(toolCall.ID)
		if toolCallID != "" {
			toolCallIDs = append(toolCallIDs, toolCallID)
		}
	}
	if len(toolCallIDs) == 0 {
		return
	}

	p.reasoningStore.StoreToolCallReasoning(sessionID, toolCallIDs, content)
}

func (p *EinoLLMProvider) requestChatModel(messages []*schema.Message, tools []*schema.ToolInfo) (model.ToolCallingChatModel, map[string]string, error) {
	if p == nil || p.chatModel == nil {
		return nil, nil, errors.New("chat model is nil")
	}

	chatModel := p.chatModel
	if len(tools) == 0 {
		return chatModel, nil, nil
	}

	if p.shouldOmitToolsOnFollowup(messages, tools) {
		log.Infof("检测到讯飞 OpenAI 兼容 follow-up 轮次，跳过 tools 绑定以避免 Bad Request")
		return chatModel, nil, nil
	}

	boundTools, toolNameAliases := p.prepareToolsForBinding(tools)
	boundModel, err := chatModel.WithTools(boundTools)
	if err != nil {
		return nil, nil, err
	}
	return boundModel, toolNameAliases, nil
}

func logToolRequestDiagnostics(sessionID string, messages []*schema.Message, requestErr error) {
	issues := detectToolMessageSequenceIssues(messages)
	if requestErr == nil && len(issues) == 0 {
		return
	}

	summary := summarizeMessagesForToolRequest(messages)
	if requestErr != nil {
		log.Errorf("[Eino-LLM] 工具请求消息诊断 - SessionID: %s, err: %v, issues: %s, summary: %s", sessionID, requestErr, strings.Join(issues, " | "), summary)
		return
	}

	log.Warnf("[Eino-LLM] 发送前检测到工具消息链异常 - SessionID: %s, issues: %s, summary: %s", sessionID, strings.Join(issues, " | "), summary)
}

func summarizeMessagesForToolRequest(messages []*schema.Message) string {
	if len(messages) == 0 {
		return "<empty>"
	}

	const maxMessages = 16
	start := 0
	if len(messages) > maxMessages {
		start = len(messages) - maxMessages
	}

	parts := make([]string, 0, len(messages)-start)
	for i := start; i < len(messages); i++ {
		msg := messages[i]
		if msg == nil {
			parts = append(parts, fmt.Sprintf("#%d:<nil>", i))
			continue
		}

		part := fmt.Sprintf("#%d:%s(len=%d", i, msg.Role, len(msg.Content))
		if len(msg.ToolCalls) > 0 {
			part += fmt.Sprintf(",tool_calls=%s", summarizeToolCallsForRequest(msg.ToolCalls))
		}
		if msg.ToolCallID != "" {
			part += fmt.Sprintf(",tool_call_id=%s", strings.TrimSpace(msg.ToolCallID))
		}
		part += fmt.Sprintf(",snippet=%q)", summarizeContentSnippet(msg.Content, 24))
		parts = append(parts, part)
	}

	if start > 0 {
		return fmt.Sprintf("... %s", strings.Join(parts, " | "))
	}
	return strings.Join(parts, " | ")
}

func summarizeToolCallsForRequest(toolCalls []schema.ToolCall) string {
	if len(toolCalls) == 0 {
		return "[]"
	}

	parts := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		parts = append(parts, fmt.Sprintf("%s:%s", strings.TrimSpace(toolCall.ID), strings.TrimSpace(toolCall.Function.Name)))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func summarizeContentSnippet(content string, maxLen int) string {
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")
	content = strings.ReplaceAll(content, "\t", " ")
	if maxLen <= 0 || len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

func detectToolMessageSequenceIssues(messages []*schema.Message) []string {
	if len(messages) == 0 {
		return nil
	}

	issues := make([]string, 0)
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg == nil {
			continue
		}

		if msg.Role == schema.Tool {
			issues = append(issues, fmt.Sprintf("orphan tool message at #%d tool_call_id=%s", i, strings.TrimSpace(msg.ToolCallID)))
			continue
		}

		if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}

		pendingToolCallIDs := make(map[string]struct{}, len(msg.ToolCalls))
		invalidToolCallID := false
		for _, toolCall := range msg.ToolCalls {
			toolCallID := strings.TrimSpace(toolCall.ID)
			if toolCallID == "" {
				invalidToolCallID = true
				break
			}
			pendingToolCallIDs[toolCallID] = struct{}{}
		}
		if invalidToolCallID || len(pendingToolCallIDs) == 0 {
			issues = append(issues, fmt.Sprintf("assistant tool_calls at #%d contains empty tool_call_id", i))
			continue
		}

		matchedTools := 0
		nextIndex := i + 1
		for ; nextIndex < len(messages); nextIndex++ {
			nextMsg := messages[nextIndex]
			if nextMsg == nil {
				continue
			}
			if nextMsg.Role != schema.Tool {
				break
			}

			toolCallID := strings.TrimSpace(nextMsg.ToolCallID)
			if _, ok := pendingToolCallIDs[toolCallID]; !ok {
				issues = append(issues, fmt.Sprintf("assistant tool_calls at #%d met unexpected tool message #%d tool_call_id=%s", i, nextIndex, toolCallID))
				break
			}

			delete(pendingToolCallIDs, toolCallID)
			matchedTools++
			if len(pendingToolCallIDs) == 0 {
				break
			}
		}

		if len(pendingToolCallIDs) > 0 {
			issues = append(issues, fmt.Sprintf("assistant tool_calls at #%d missing %d tool responses before next non-tool message", i, len(pendingToolCallIDs)))
		}

		if len(pendingToolCallIDs) == 0 {
			i = nextIndex
			continue
		}

		if matchedTools > 0 {
			i = nextIndex - 1
		}
	}

	return issues
}

func (p *EinoLLMProvider) prepareToolsForBinding(tools []*schema.ToolInfo) ([]*schema.ToolInfo, map[string]string) {
	if len(tools) == 0 || p == nil || !p.requiresOpenAICompatibleToolNames() {
		return tools, nil
	}

	prepared := make([]*schema.ToolInfo, 0, len(tools))
	usedAliases := make(map[string]string, len(tools))
	aliasToOriginal := make(map[string]string)
	rewrittenCount := 0

	for _, toolInfo := range tools {
		if toolInfo == nil {
			prepared = append(prepared, nil)
			continue
		}

		originalName := strings.TrimSpace(toolInfo.Name)
		alias := originalName
		if !isOpenAICompatibleToolName(alias) {
			alias = sanitizeOpenAIToolNameCandidate(alias)
		}
		alias = ensureUniqueToolAlias(alias, originalName, usedAliases)

		if alias != originalName {
			cloned := *toolInfo
			cloned.Name = alias
			prepared = append(prepared, &cloned)
			aliasToOriginal[alias] = originalName
			rewrittenCount++
			continue
		}

		prepared = append(prepared, toolInfo)
	}

	if rewrittenCount > 0 {
		log.Infof("为兼容 OpenAI 工具名约束，已重写 %d 个工具名", rewrittenCount)
	}
	if len(aliasToOriginal) == 0 {
		return tools, nil
	}
	return prepared, aliasToOriginal
}

func (p *EinoLLMProvider) requiresOpenAICompatibleToolNames() bool {
	return p != nil && strings.EqualFold(strings.TrimSpace(p.providerType), "openai")
}

func restoreOriginalToolCallNames(message *schema.Message, aliasToOriginal map[string]string) *schema.Message {
	if message == nil || len(aliasToOriginal) == 0 || len(message.ToolCalls) == 0 {
		return message
	}

	changed := false
	toolCalls := make([]schema.ToolCall, len(message.ToolCalls))
	for i, toolCall := range message.ToolCalls {
		toolCalls[i] = toolCall
		alias := strings.TrimSpace(toolCall.Function.Name)
		originalName, ok := aliasToOriginal[alias]
		if !ok || originalName == alias {
			continue
		}

		toolCalls[i].Function.Name = originalName
		changed = true
	}

	if !changed {
		return message
	}

	cloned := *message
	cloned.ToolCalls = toolCalls
	return &cloned
}

func isOpenAICompatibleToolName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}

	for _, r := range name {
		if !isOpenAICompatibleToolNameRune(r) {
			return false
		}
	}
	return true
}

func isOpenAICompatibleToolNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '_' || r == '-':
		return true
	default:
		return false
	}
}

func sanitizeOpenAIToolNameCandidate(name string) string {
	var builder strings.Builder
	builder.Grow(len(name))

	lastWasUnderscore := false
	for _, r := range name {
		if isOpenAICompatibleToolNameRune(r) {
			builder.WriteRune(r)
			lastWasUnderscore = false
			continue
		}
		if lastWasUnderscore {
			continue
		}
		builder.WriteByte('_')
		lastWasUnderscore = true
	}

	sanitized := strings.Trim(builder.String(), "_-")
	if sanitized == "" {
		return "tool"
	}
	return sanitized
}

func ensureUniqueToolAlias(alias string, originalName string, usedAliases map[string]string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = "tool"
	}

	if usedBy, exists := usedAliases[alias]; !exists || usedBy == originalName {
		usedAliases[alias] = originalName
		return alias
	}

	digest := sha1.Sum([]byte(originalName))
	for prefixLen := 4; prefixLen <= len(digest); prefixLen += 2 {
		candidate := fmt.Sprintf("%s_%x", alias, digest[:prefixLen])
		if usedBy, exists := usedAliases[candidate]; !exists || usedBy == originalName {
			usedAliases[candidate] = originalName
			return candidate
		}
	}

	for counter := 1; ; counter++ {
		candidate := fmt.Sprintf("%s_%d", alias, counter)
		if usedBy, exists := usedAliases[candidate]; !exists || usedBy == originalName {
			usedAliases[candidate] = originalName
			return candidate
		}
	}
}

func (p *EinoLLMProvider) shouldOmitToolsOnFollowup(messages []*schema.Message, tools []*schema.ToolInfo) bool {
	if len(tools) == 0 || !hasToolResultMessage(messages) {
		return false
	}

	return p.isXunfeiOpenAICompatTarget()
}

func hasToolResultMessage(messages []*schema.Message) bool {
	for _, msg := range messages {
		if msg != nil && msg.Role == schema.Tool {
			return true
		}
	}
	return false
}

func (p *EinoLLMProvider) isXunfeiOpenAICompatTarget() bool {
	if p == nil {
		return false
	}

	provider := strings.ToLower(strings.TrimSpace(getConfigString(p.config, "provider")))
	if provider == "xunfei" {
		return true
	}

	baseURL := strings.ToLower(strings.TrimSpace(getConfigString(p.config, "base_url")))
	return strings.Contains(baseURL, "xf-yun.com")
}

func getConfigString(config map[string]interface{}, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func (p *EinoLLMProvider) buildModelCallOptions() []model.Option {
	if p == nil || p.maxTokens <= 0 {
		return nil
	}

	provider := ""
	if p.config != nil {
		if rawProvider, ok := p.config["provider"].(string); ok {
			provider = rawProvider
		}
	}

	if shouldUseMaxCompletionTokens(provider, p.modelName) {
		return nil
	}

	return []model.Option{model.WithMaxTokens(p.maxTokens)}
}

// isValidJSON 检查字符串是否是有效的JSON
func isValidJSON(str string) bool {
	var js map[string]interface{}
	return json.Unmarshal([]byte(str), &js) == nil
}

// GetChatModel 获取底层的Eino ChatModel
func (p *EinoLLMProvider) GetChatModel() model.ToolCallingChatModel {
	return p.chatModel
}

// GetProviderType 获取提供者类型
func (p *EinoLLMProvider) GetProviderType() string {
	return p.providerType
}

// WithMaxTokens 设置最大令牌数
func (p *EinoLLMProvider) WithMaxTokens(maxTokens int) *EinoLLMProvider {
	newProvider := *p
	newProvider.maxTokens = maxTokens
	return &newProvider
}

// WithStreamable 设置是否支持流式
func (p *EinoLLMProvider) WithStreamable(streamable bool) *EinoLLMProvider {
	newProvider := *p
	newProvider.streamable = streamable
	return &newProvider
}

// Close 关闭资源（无状态 Provider，无需关闭）
func (p *EinoLLMProvider) Close() error {
	return nil
}

// IsValid 检查资源是否有效
func (p *EinoLLMProvider) IsValid() bool {
	return p != nil && p.chatModel != nil
}
