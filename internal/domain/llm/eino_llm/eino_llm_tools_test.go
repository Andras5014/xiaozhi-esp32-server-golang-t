package eino_llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

type stubToolCallingChatModel struct {
	generateResp   *schema.Message
	generateErr    error
	withToolsResp  model.ToolCallingChatModel
	withToolsErr   error
	withToolsFunc  func([]*schema.ToolInfo) (model.ToolCallingChatModel, error)
	capturedTools  []*schema.ToolInfo
	generateCalls  int
	withToolsCalls int
}

func (s *stubToolCallingChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	s.generateCalls++
	return s.generateResp, s.generateErr
}

func (s *stubToolCallingChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not implemented in stub")
}

func (s *stubToolCallingChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	s.withToolsCalls++
	s.capturedTools = cloneToolInfos(tools)
	if s.withToolsFunc != nil {
		return s.withToolsFunc(tools)
	}
	if s.withToolsResp != nil || s.withToolsErr != nil {
		return s.withToolsResp, s.withToolsErr
	}
	return s, nil
}

func TestEinoResponseWithTools_DoesNotPersistBoundTools(t *testing.T) {
	baseModel := &stubToolCallingChatModel{
		generateResp: schema.AssistantMessage("base", nil),
	}
	boundModel := &stubToolCallingChatModel{
		generateResp: schema.AssistantMessage("bound", nil),
	}
	baseModel.withToolsResp = boundModel

	provider := &EinoLLMProvider{
		chatModel:    baseModel,
		maxTokens:    128,
		streamable:   false,
		config:       map[string]interface{}{"provider": "openai"},
		providerType: "openai",
	}

	messages := []*schema.Message{schema.UserMessage("hello")}
	tools := []*schema.ToolInfo{{Name: "get_weather", ParamsOneOf: &schema.ParamsOneOf{}}}

	first := collectProviderResponses(provider.ResponseWithContext(context.Background(), "tool-call", messages, tools))
	second := collectProviderResponses(provider.ResponseWithContext(context.Background(), "plain-call", messages, nil))

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.Equal(t, "bound", first[0].Content)
	require.Equal(t, "base", second[0].Content)
	require.Equal(t, 1, baseModel.withToolsCalls)
	require.Equal(t, 1, baseModel.generateCalls)
	require.Equal(t, 1, boundModel.generateCalls)
	require.Same(t, baseModel, provider.chatModel)
}

func TestEinoResponseWithTools_OmitsToolsOnXunfeiFollowup(t *testing.T) {
	baseModel := &stubToolCallingChatModel{
		generateResp: schema.AssistantMessage("followup answer", nil),
	}
	boundModel := &stubToolCallingChatModel{
		generateResp: schema.AssistantMessage("should not be used", nil),
	}
	baseModel.withToolsResp = boundModel

	provider := &EinoLLMProvider{
		chatModel:    baseModel,
		maxTokens:    128,
		streamable:   false,
		config:       map[string]interface{}{"provider": "openai", "base_url": "https://maas-coding-api.cn-huabei-1.xf-yun.com/v2"},
		providerType: "openai",
	}

	toolCallID := "call_probe"
	messages := []*schema.Message{
		schema.SystemMessage("你是一个中文助手。"),
		schema.UserMessage("一级方案有多少"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:   toolCallID,
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "get_research_plan_statistics",
				Arguments: `{"question":"一级方案有多少"}`,
			},
		}}),
		schema.ToolMessage(`{"ok":true}`, toolCallID),
	}
	tools := []*schema.ToolInfo{{Name: "get_research_plan_statistics", ParamsOneOf: &schema.ParamsOneOf{}}}

	responses := collectProviderResponses(provider.ResponseWithContext(context.Background(), "xunfei-followup", messages, tools))

	require.Len(t, responses, 1)
	require.Equal(t, "followup answer", responses[0].Content)
	require.Equal(t, 0, baseModel.withToolsCalls)
	require.Equal(t, 1, baseModel.generateCalls)
	require.Equal(t, 0, boundModel.generateCalls)
}

func TestEinoResponseWithTools_RewritesOpenAIToolNamesRoundTrip(t *testing.T) {
	baseModel := &stubToolCallingChatModel{}
	baseModel.withToolsFunc = func(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
		require.Len(t, tools, 2)
		require.Equal(t, "self_bazi_get_bazi_detail", tools[0].Name)
		require.NotEqual(t, tools[0].Name, tools[1].Name)
		require.True(t, strings.HasPrefix(tools[1].Name, "self_bazi_get_bazi_detail_"))

		return &stubToolCallingChatModel{
			generateResp: schema.AssistantMessage("", []schema.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      tools[0].Name,
					Arguments: `{"question":"test"}`,
				},
			}}),
		}, nil
	}

	provider := &EinoLLMProvider{
		chatModel:    baseModel,
		maxTokens:    128,
		streamable:   false,
		config:       map[string]interface{}{"provider": "openai"},
		providerType: "openai",
	}

	tools := []*schema.ToolInfo{
		{Name: "self.bazi.get_bazi_detail", ParamsOneOf: &schema.ParamsOneOf{}},
		{Name: "self_bazi_get_bazi_detail", ParamsOneOf: &schema.ParamsOneOf{}},
	}

	responses := collectProviderResponses(provider.ResponseWithContext(context.Background(), "openai-tool-alias", []*schema.Message{schema.UserMessage("hi")}, tools))

	require.Len(t, responses, 1)
	require.Len(t, responses[0].ToolCalls, 1)
	require.Equal(t, "self.bazi.get_bazi_detail", responses[0].ToolCalls[0].Function.Name)
}

func TestDetectToolMessageSequenceIssuesFlagsMissingToolResponse(t *testing.T) {
	messages := []*schema.Message{
		schema.SystemMessage("你是一个中文助手。"),
		schema.UserMessage("帮我查一下桥梁长度"),
		schema.AssistantMessage("", []schema.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "execute",
					Arguments: `{"api_name":"CountQuery"}`,
				},
			},
			{
				ID:   "call_2",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "execute",
					Arguments: `{"api_name":"CountQuery"}`,
				},
			},
		}),
		schema.ToolMessage(`{"ok":true}`, "call_1"),
		schema.AssistantMessage("继续补充说明", nil),
	}

	issues := detectToolMessageSequenceIssues(messages)

	require.NotEmpty(t, issues)
	require.True(t, strings.Contains(strings.Join(issues, " | "), "missing 1 tool responses"))
}

func cloneToolInfos(tools []*schema.ToolInfo) []*schema.ToolInfo {
	if len(tools) == 0 {
		return nil
	}

	cloned := make([]*schema.ToolInfo, len(tools))
	for i, toolInfo := range tools {
		if toolInfo == nil {
			continue
		}
		copy := *toolInfo
		cloned[i] = &copy
	}
	return cloned
}

func collectProviderResponses(ch chan *schema.Message) []*schema.Message {
	var messages []*schema.Message
	for msg := range ch {
		if msg != nil {
			messages = append(messages, msg)
		}
	}
	return messages
}
