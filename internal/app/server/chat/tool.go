package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	data_client "xiaozhi-esp32-server-golang/internal/data/client"
	"xiaozhi-esp32-server-golang/internal/domain/eventbus"
	llm_common "xiaozhi-esp32-server-golang/internal/domain/llm/common"
	"xiaozhi-esp32-server-golang/internal/domain/mcp"
	log "xiaozhi-esp32-server-golang/logger"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	mcp_go "github.com/mark3labs/mcp-go/mcp"
)

type toolCallResponseSummary struct {
	invokeToolSuccess bool
	hasMediaOutput    bool
}

// handleToolCallResponse 处理工具调用响应
func (l *LLMManager) handleToolCallResponse(ctx context.Context, respMsg *schema.Message, tools []schema.ToolCall, executor *toolCallExecutor) (toolCallResponseSummary, error) {
	if len(tools) == 0 {
		return toolCallResponseSummary{}, nil
	}

	log.Infof("处理 %d 个工具调用", len(tools))
	if executor == nil {
		executor = newToolCallExecutor(l, ctx)
		executor.Submit(tools)
	}

	var messageList []*schema.Message

	// 只有当respMsg有内容（Content不为空或ToolCalls不为空）时才添加到messageList
	// 避免保存空的assistant消息导致后续LLM调用出现400错误
	if respMsg != nil && (respMsg.Content != "" || len(respMsg.ToolCalls) > 0) {
		messageList = append(messageList, respMsg)
	}

	invokeToolSuccess := false
	findExitTool := false
	shouldStopLLMProcessing := false
	hasMediaOutput := false

	results := executor.Wait()
	for _, result := range results {
		if result.invokeToolSuccess {
			invokeToolSuccess = true
		}
		if result.findExitTool {
			findExitTool = true
		}
		if result.shouldStopLLMProcessing {
			shouldStopLLMProcessing = true
		}
		if result.hasMediaOutput {
			hasMediaOutput = true
		}
		messageList = append(messageList, result.message)
	}

	if len(messageList) > 0 {
		for _, msg := range messageList {
			// 过滤掉Content为空的assistant消息，避免保存到历史记录中
			// 空的assistant消息会导致后续LLM调用时出现400错误
			if msg != nil && msg.Role == schema.Assistant && msg.Content == "" && len(msg.ToolCalls) == 0 {
				log.Debugf("跳过保存空的assistant消息")
				continue
			}
			l.AddLlmMessage(ctx, msg)
		}
	}

	if l.clientState.GetMemoryMode() == data_client.MemoryModeNone {
		ctx = appendToolRoundMessagesToContext(ctx, messageList)
	}

	executor.WaitMedia()

	if findExitTool {
		// 发布退出聊天事件
		eventbus.Get().Publish(eventbus.TopicExitChat, &eventbus.ExitChatEvent{
			ClientState: l.clientState,
			Reason:      "工具调用退出",
			TriggerType: "tool_call",
			UserText:    "",
			Timestamp:   time.Now(),
		})

		return toolCallResponseSummary{
			invokeToolSuccess: invokeToolSuccess,
			hasMediaOutput:    hasMediaOutput,
		}, nil
	}

	// 如果工具调用成功且没有被标记为停止处理，则继续LLM调用
	if invokeToolSuccess && !shouldStopLLMProcessing {
		if err := l.continueToolCallLLMResponse(ctx); err != nil {
			log.Warnf("工具调用后二次 LLM 续答失败，使用本地回退响应: %v", err)
			fallbackText := buildToolFallbackText(results)
			if strings.TrimSpace(fallbackText) == "" {
				return toolCallResponseSummary{
					invokeToolSuccess: invokeToolSuccess,
					hasMediaOutput:    hasMediaOutput,
				}, err
			}
			if fallbackErr := l.emitToolFallbackResponse(ctx, fallbackText); fallbackErr != nil {
				return toolCallResponseSummary{
					invokeToolSuccess: invokeToolSuccess,
					hasMediaOutput:    hasMediaOutput,
				}, fmt.Errorf("工具续答失败: %v; 本地回退失败: %w", err, fallbackErr)
			}
		}
	}

	return toolCallResponseSummary{
		invokeToolSuccess: invokeToolSuccess,
		hasMediaOutput:    hasMediaOutput,
	}, nil
}

type toolCallExecutionResult struct {
	order                   int
	message                 *schema.Message
	invokeToolSuccess       bool
	findExitTool            bool
	shouldStopLLMProcessing bool
	hasMediaOutput          bool
}

type toolCallExecutor struct {
	manager *LLMManager
	ctx     context.Context
	toolCtx context.Context

	mu             sync.Mutex
	submittedCall  map[string]struct{}
	submitSequence int
	results        []toolCallExecutionResult

	wg      sync.WaitGroup
	mediaWg sync.WaitGroup
}

func newToolCallExecutor(manager *LLMManager, ctx context.Context) *toolCallExecutor {
	toolCtx := ctx
	if chatSessionOperator, ok := ctx.Value("chat_session_operator").(ChatSessionOperator); ok {
		toolCtx = context.WithValue(ctx, "chat_session_operator", chatSessionOperator)
	}

	return &toolCallExecutor{
		manager:       manager,
		ctx:           ctx,
		toolCtx:       toolCtx,
		submittedCall: make(map[string]struct{}),
	}
}

func (e *toolCallExecutor) Submit(toolCalls []schema.ToolCall) {
	for _, tc := range toolCalls {
		callID := tc.ID
		if callID == "" {
			callID = fmt.Sprintf("auto_%s_%s", tc.Function.Name, tc.Function.Arguments)
		}

		e.mu.Lock()
		if _, exists := e.submittedCall[callID]; exists {
			e.mu.Unlock()
			continue
		}
		e.submittedCall[callID] = struct{}{}
		order := e.submitSequence
		e.submitSequence++
		e.wg.Add(1)
		e.mu.Unlock()

		toolCall := tc
		go func() {
			defer e.wg.Done()
			result := e.executeToolCall(order, toolCall)

			e.mu.Lock()
			e.results = append(e.results, result)
			e.mu.Unlock()
		}()
	}
}

func (e *toolCallExecutor) Wait() []toolCallExecutionResult {
	e.wg.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	results := append([]toolCallExecutionResult(nil), e.results...)
	sort.Slice(results, func(i, j int) bool {
		return results[i].order < results[j].order
	})
	return results
}

func (e *toolCallExecutor) WaitMedia() {
	e.mediaWg.Wait()
}

func (e *toolCallExecutor) executeToolCall(order int, toolCall schema.ToolCall) toolCallExecutionResult {
	resultMessage := &schema.Message{
		Role:       schema.Tool,
		ToolCallID: toolCall.ID,
	}

	state := e.manager.clientState
	toolName := toolCall.Function.Name
	transportType := ""
	if e.manager != nil && e.manager.serverTransport != nil {
		transportType = e.manager.serverTransport.GetTransportType()
	}
	toolObj, ok := mcp.GetToolByNameWithTransport(state.DeviceID, state.AgentID, transportType, toolName, state.DeviceConfig.MCPServiceNames)
	if !ok || toolObj == nil {
		log.Errorf("未找到工具: %s", toolName)
		resultMessage.Content = fmt.Sprintf("未找到工具: %s", toolName)
		return toolCallExecutionResult{order: order, message: resultMessage}
	}

	log.Infof("进行工具调用请求: %s, 参数: %+v", toolName, toolCall.Function.Arguments)
	startTs := time.Now().UnixMilli()
	fcResult, err := toolObj.InvokableRun(e.toolCtx, toolCall.Function.Arguments)
	if err != nil {
		log.Errorf("工具调用失败: %v", err)
		resultMessage.Content = fmt.Sprintf("工具 %s 调用失败: %v", toolName, err)
		return toolCallExecutionResult{order: order, message: resultMessage}
	}
	costTs := time.Now().UnixMilli() - startTs
	if len(fcResult) > 2048 {
		log.Infof("工具调用结果 len: %d, 耗时: %dms", len(fcResult), costTs)
	} else {
		log.Infof("工具调用结果 %s, 耗时: %dms", fcResult, costTs)
	}

	execResult := toolCallExecutionResult{
		order:             order,
		message:           resultMessage,
		invokeToolSuccess: true,
	}
	normalizedContent := fcResult

	var contentList []mcp_go.Content
	if mcpResp, ok := e.manager.handleLocalToolResult(fcResult); ok {
		if mcpResp.GetType() == MCPResponseTypeAction && mcpResp.GetAction() == "exit_conversation" {
			execResult.findExitTool = true
		}
		if actionResp, ok := mcpResp.(*MCPActionResponse); ok {
			if actionResp.FinalAction || actionResp.NoFurtherResponse || actionResp.SilenceLLM {
				execResult.shouldStopLLMProcessing = true
			}
		}
		contentList = mcpResp.GetContent()
	} else if toolCallResult, ok := e.manager.handleToolResult(fcResult); ok {
		if toolCallResult.IsError {
			log.Errorf("工具调用失败: %s, 错误标记: %t", fcResult, toolCallResult.IsError)
		}
		contentList = toolCallResult.Content
	}

	if len(contentList) > 0 {
		var mcpContent string
		for _, content := range contentList {
			if audioContent, ok := content.(mcp_go.AudioContent); ok {
				log.Debugf("调用工具 %s 返回音频资源长度: %d", toolName, len(audioContent.Data))
				mcpContent = "执行成功"
				if err := e.manager.handleAudioContent(e.ctx, toolName, audioContent, &e.mediaWg); err != nil {
					log.Errorf("mcp播放音频资源失败: %v", err)
					mcpContent = "执行失败"
				}
				execResult.shouldStopLLMProcessing = true
				execResult.hasMediaOutput = true
				break
			}
			if resourceLink, ok := content.(mcp_go.ResourceLink); ok {
				log.Debugf("调用工具 %s 返回资源链接: %+v", toolName, resourceLink)
				mcpContent = "执行成功"
				if err := e.manager.handleResourceLink(e.ctx, resourceLink, toolObj, &e.mediaWg); err != nil {
					log.Errorf("mcp播放资源链接失败: %v", err)
					mcpContent = "执行失败"
				}
				execResult.shouldStopLLMProcessing = true
				execResult.hasMediaOutput = true
				break
			}
			if textContent, ok := content.(mcp_go.TextContent); ok {
				log.Debugf("调用工具 %s 返回文本资源长度: %s", toolName, textContent.Text)
				mcpContent += textContent.Text
			}
		}
		if mcpContent != "" {
			normalizedContent = mcpContent
		}
	}

	execResult.message.Content = normalizedContent
	return execResult
}

func (l *LLMManager) handleResourceLink(ctx context.Context, resourceLink mcp_go.ResourceLink, toolCall tool.InvokableTool, wg *sync.WaitGroup) error {
	wg.Add(1)

	source, err := buildMediaSourceFromResourceLink(resourceLink, toolCall)
	if err != nil {
		wg.Done()
		return err
	}

	if l.session == nil || l.session.mediaPlayer == nil {
		wg.Done()
		return fmt.Errorf("session media player 未初始化")
	}

	handle, err := l.session.mediaPlayer.PlaySourceWithHandle(ctx, source)
	if err != nil {
		wg.Done()
		return err
	}

	go func() {
		defer wg.Done()
		_ = handle.Wait(ctx)
	}()

	return nil
}

func (l *LLMManager) handleAudioContent(ctx context.Context, realMusicName string, audioContent mcp_go.AudioContent, wg *sync.WaitGroup) error {
	wg.Add(1)

	source, err := buildMediaSourceFromAudioContent(realMusicName, audioContent)
	if err != nil {
		wg.Done()
		log.Errorf("解码音频数据失败: %v", err)
		return err
	}

	if l.session == nil || l.session.mediaPlayer == nil {
		wg.Done()
		return fmt.Errorf("session media player 未初始化")
	}

	handle, err := l.session.mediaPlayer.PlaySourceWithHandle(ctx, source)
	if err != nil {
		wg.Done()
		return err
	}

	go func() {
		defer wg.Done()
		_ = handle.Wait(ctx)
	}()

	return nil
}

func buildMediaSourceFromAudioContent(title string, audioContent mcp_go.AudioContent) (MediaSourceDescriptor, error) {
	rawAudioData, err := base64.StdEncoding.DecodeString(audioContent.Data)
	if err != nil {
		return MediaSourceDescriptor{}, fmt.Errorf("解码音频数据失败: %v", err)
	}

	title = strings.TrimSpace(title)
	if title == "" || title == "执行成功" {
		title = "工具音频"
	}

	return MediaSourceDescriptor{
		Title:      title,
		MIMEType:   audioContent.MIMEType,
		SourceType: MediaSourceTypeInlineAudio,
		Meta: map[string]string{
			"mime_type": audioContent.MIMEType,
			"source":    string(MediaSourceTypeInlineAudio),
		},
		Inline: &InlineAudioSource{
			Data: rawAudioData,
		},
	}, nil
}

func buildMediaSourceFromResourceLink(resourceLink mcp_go.ResourceLink, toolCall tool.InvokableTool) (MediaSourceDescriptor, error) {
	mcpTool, ok := toolCall.(*mcp.McpTool)
	if !ok || mcpTool == nil {
		return MediaSourceDescriptor{}, fmt.Errorf("resource link 播放仅支持 MCP 远程工具")
	}

	serverName := mcpTool.GetServerName()
	endpointSnapshot := mcp.GetServerEndpointSnapshotByName(serverName)
	directAudioURL := strings.TrimSpace(resourceLink.Description)
	readArgs := make(map[string]any)
	if directAudioURL != "" {
		readArgs["url"] = directAudioURL
	}

	title := strings.TrimSpace(resourceLink.Name)
	if title == "" {
		title = strings.TrimSpace(resourceLink.Description)
	}
	if title == "" {
		title = strings.TrimSpace(resourceLink.URI)
	}

	toolName := ""
	if info, err := mcpTool.Info(context.Background()); err == nil && info != nil {
		toolName = info.Name
	}

	return MediaSourceDescriptor{
		Title:      title,
		MIMEType:   resourceLink.MIMEType,
		SourceType: MediaSourceTypeMCPResource,
		Meta: map[string]string{
			"source":       string(MediaSourceTypeMCPResource),
			"server_name":  serverName,
			"endpoint":     endpointSnapshot,
			"resource_uri": resourceLink.URI,
			"audio_url":    directAudioURL,
			"tool_name":    toolName,
		},
		MCP: &MCPMediaSource{
			ServerName:       serverName,
			EndpointSnapshot: endpointSnapshot,
			ToolName:         toolName,
			ResourceURI:      resourceLink.URI,
			DirectAudioURL:   directAudioURL,
			Description:      resourceLink.Description,
			ReadArgs:         readArgs,
			PageSize:         McpReadResourcePageSize,
			Client:           mcpTool.GetClient(),
		},
	}, nil
}

func (l *LLMManager) handleLocalToolResult(toolResult string) (MCPResponse, bool) {
	// 如果是mcp返回格式, 则解析
	var response MCPResponse
	var err error
	if response, err = ParseMCPResponse(toolResult); err != nil {
		return nil, false
	}
	return response, true
}

func (l *LLMManager) handleToolResult(toolResultStr string) (mcp_go.CallToolResult, bool) {
	var toolResult mcp_go.CallToolResult
	if err := json.Unmarshal([]byte(toolResultStr), &toolResult); err != nil {
		log.Debugf("Failed to unmarshal tool result: %v", err)
		toolResult.Content = []mcp_go.Content{
			mcp_go.TextContent{
				Type: "text",
				Text: toolResultStr,
			},
		}
		return toolResult, true
	}

	return toolResult, true
}

func (l *LLMManager) continueToolCallLLMResponse(ctx context.Context) error {
	err := l.DoLLmRequest(ctx, nil, l.einoTools, true, nil)
	if err == nil || len(l.einoTools) == 0 {
		return err
	}

	log.Warnf("工具调用后带工具续答失败，尝试无工具续答: %v", err)
	retryErr := l.DoLLmRequest(ctx, nil, nil, true, nil)
	if retryErr == nil {
		return nil
	}

	return fmt.Errorf("带工具续答失败: %v; 无工具重试失败: %w", err, retryErr)
}

func (l *LLMManager) emitToolFallbackResponse(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("工具回退内容为空")
	}

	fallbackChan := make(chan llm_common.LLMResponseStruct, 1)
	fallbackChan <- llm_common.LLMResponseStruct{
		Text:    text,
		IsStart: true,
		IsEnd:   true,
	}
	close(fallbackChan)

	_, err := l.HandleLLMResponseChannelSync(ctx, nil, fallbackChan, nil)
	return err
}

type structuredToolFallbackPayload struct {
	Question      string                       `json:"question"`
	Message       string                       `json:"message"`
	Summary       map[string]interface{}       `json:"summary"`
	MatchedFields []structuredToolMatchedField `json:"matched_fields"`
}

type structuredToolMatchedField struct {
	Field string      `json:"field"`
	Label string      `json:"label"`
	Value interface{} `json:"value"`
}

func buildToolFallbackText(results []toolCallExecutionResult) string {
	if len(results) == 0 {
		return ""
	}

	parts := make([]string, 0, len(results))
	for _, result := range results {
		if result.message == nil {
			continue
		}
		content := normalizeToolFallbackText(result.message.Content)
		if content == "" {
			continue
		}
		parts = append(parts, content)
	}

	return strings.Join(parts, "\n")
}

func normalizeToolFallbackText(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if summary := formatStructuredToolFallback(raw); summary != "" {
		return summary
	}

	return raw
}

func formatStructuredToolFallback(raw string) string {
	var payload structuredToolFallbackPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || len(payload.Summary) == 0 {
		return ""
	}

	if key, value, ok := selectToolFallbackSummaryField(payload.Question, payload.MatchedFields, payload.Summary); ok {
		return fmt.Sprintf("%s：%s", key, formatToolFallbackValue(value))
	}

	return "查询结果：" + formatToolFallbackSummary(payload.Summary)
}

func selectToolFallbackSummaryField(question string, matchedFields []structuredToolMatchedField, summary map[string]interface{}) (string, interface{}, bool) {
	for _, field := range matchedFields {
		label := strings.TrimSpace(field.Label)
		if label != "" {
			if value, ok := summary[label]; ok {
				return label, value, true
			}
			if field.Value != nil {
				return label, field.Value, true
			}
		}

		name := strings.TrimSpace(field.Field)
		if name != "" {
			if value, ok := summary[name]; ok {
				return name, value, true
			}
		}
	}

	normalizedQuestion := normalizeToolFallbackMetricLabel(question)
	if normalizedQuestion == "" {
		return "", nil, false
	}

	keys := make([]string, 0, len(summary))
	for key := range summary {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		normalizedKey := normalizeToolFallbackMetricLabel(key)
		if normalizedKey == "" {
			continue
		}
		if strings.Contains(normalizedQuestion, normalizedKey) || strings.Contains(normalizedKey, normalizedQuestion) {
			return key, summary[key], true
		}
	}

	if len(keys) == 1 {
		key := keys[0]
		return key, summary[key], true
	}

	return "", nil, false
}

func normalizeToolFallbackMetricLabel(s string) string {
	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\r", "",
		"\n", "",
		"超危险", "超危大",
		"危险", "危大",
		"总数", "",
		"数量", "",
		"个数", "",
		"多少", "",
	)
	return replacer.Replace(strings.ToLower(strings.TrimSpace(s)))
}

func formatToolFallbackSummary(summary map[string]interface{}) string {
	if len(summary) == 0 {
		return ""
	}

	keys := make([]string, 0, len(summary))
	for key := range summary {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s %s", key, formatToolFallbackValue(summary[key])))
	}

	return strings.Join(parts, "；")
}

func formatToolFallbackValue(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}
