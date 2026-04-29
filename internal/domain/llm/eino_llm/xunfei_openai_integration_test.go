package eino_llm

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

const (
	xunfeiOpenAIRunEnv      = "XUNFEI_OPENAI_RUN"
	xunfeiOpenAIBaseURLEnv  = "XUNFEI_OPENAI_BASE_URL"
	xunfeiOpenAIAPIKeyEnv   = "XUNFEI_OPENAI_API_KEY"
	xunfeiOpenAIModelEnv    = "XUNFEI_OPENAI_MODEL"
	xunfeiOpenAIProviderEnv = "XUNFEI_OPENAI_PROVIDER"
	xunfeiOpenAITimeoutEnv  = "XUNFEI_OPENAI_TIMEOUT_SECONDS"
	xunfeiOpenAIMaxTokens   = "XUNFEI_OPENAI_MAX_TOKENS"
)

type xunfeiOpenAIIntegrationConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	Provider   string
	Timeout    time.Duration
	MaxTokens  int
	Streamable bool
}

func TestXunfeiOpenAICompatProbe(t *testing.T) {
	cfg := loadXunfeiOpenAIIntegrationConfig(t)

	provider, err := NewEinoLLMProvider(map[string]interface{}{
		"type":       "openai",
		"provider":   cfg.Provider,
		"model_name": cfg.Model,
		"api_key":    cfg.APIKey,
		"base_url":   cfg.BaseURL,
		"max_tokens": cfg.MaxTokens,
		"streamable": cfg.Streamable,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	tools := []*schema.ToolInfo{buildResearchPlanStatisticsToolInfo()}
	toolCallID := "call_probe_research_plan_statistics"
	toolCall := schema.ToolCall{
		ID:   toolCallID,
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "get_research_plan_statistics",
			Arguments: `{"question":"一级方案有多少"}`,
		},
	}
	toolResult := `{"content":[{"type":"text","text":"{
  \"question\": \"一级方案有多少\",
  \"fetched_at\": \"2026-04-21T04:35:24.461160+00:00\",
  \"message\": \"操作成功\",
  \"summary\": {
    \"1级方案总数\": 7,
    \"2级方案总数\": 15,
    \"3级方案总数\": 3,
    \"4A级方案总数\": 0,
    \"4B级方案总数\": 4,
    \"一般方案总数\": 1349,
    \"危大方案总数\": 2948,
    \"超危大方案总数\": 2912,
    \"专项施工方案总数\": 7231,
    \"施工组织设计方案总数\": 401,
    \"总体施工组织设计方案总数\": 89
  },
  \"matched_fields\": [
    {
      \"field\": \"levelOneNum\",
      \"label\": \"1级方案总数\",
      \"value\": 7,
      \"matched_aliases\": [\"一级方案\"]
    }
  ],
  \"data\": {
    \"levelOneNum\": 7,
    \"levelTwoNum\": 15,
    \"levelThreeNum\": 3,
    \"levelFourANum\": 0,
    \"levelFourBNum\": 4,
    \"generalNum\": 1349,
    \"dangerNum\": 2948,
    \"superDangerNum\": 2912,
    \"specialNum\": 7231,
    \"constructionNum\": 401,
    \"overallNum\": 89
  },
  \"guidance\": \"请基于 matched_fields 和 summary 回答用户。如果 matched_fields 为空，说明问题没有精确命中单个字段，请结合 summary/data 给出中文回答。\"
}"}]}`

	t.Run("plain_text", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		messages := []*schema.Message{
			schema.SystemMessage("你是一个中文助手。"),
			schema.UserMessage("只回复“收到”。"),
		}

		resp, err := collectProbeResponses(ctx, provider.ResponseWithContext(ctx, "xunfei-plain-text", messages, nil))
		logProbeResult(t, resp, err)
		if err != nil {
			t.Fatalf("plain_text failed: %v", err)
		}
	})

	t.Run("tool_first_round", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		messages := []*schema.Message{
			schema.SystemMessage("你是一个中文助手。能用工具时优先调用工具。"),
			schema.UserMessage("一级方案有多少"),
		}

		resp, err := collectProbeResponses(ctx, provider.ResponseWithContext(ctx, "xunfei-tool-first-round", messages, tools))
		logProbeResult(t, resp, err)
		if err != nil {
			t.Fatalf("tool_first_round failed: %v", err)
		}
	})

	t.Run("tool_followup_with_tools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		messages := []*schema.Message{
			schema.SystemMessage("你是一个中文助手。请根据工具结果直接回答用户。"),
			schema.UserMessage("一级方案有多少"),
			schema.AssistantMessage("", []schema.ToolCall{toolCall}),
			schema.ToolMessage(toolResult, toolCallID),
		}

		resp, err := collectProbeResponses(ctx, provider.ResponseWithContext(ctx, "xunfei-tool-followup-with-tools", messages, tools))
		logProbeResult(t, resp, err)
		if err != nil {
			t.Fatalf("tool_followup_with_tools failed: %v", err)
		}
	})

	t.Run("tool_followup_without_tools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		messages := []*schema.Message{
			schema.SystemMessage("你是一个中文助手。请根据工具结果直接回答用户。"),
			schema.UserMessage("一级方案有多少"),
			schema.AssistantMessage("", []schema.ToolCall{toolCall}),
			schema.ToolMessage(toolResult, toolCallID),
		}

		resp, err := collectProbeResponses(ctx, provider.ResponseWithContext(ctx, "xunfei-tool-followup-without-tools", messages, nil))
		logProbeResult(t, resp, err)
		if err != nil {
			t.Fatalf("tool_followup_without_tools failed: %v", err)
		}
	})
}

func loadXunfeiOpenAIIntegrationConfig(t *testing.T) xunfeiOpenAIIntegrationConfig {
	t.Helper()

	if strings.TrimSpace(os.Getenv(xunfeiOpenAIRunEnv)) == "" {
		t.Skipf("set %s=1 to run Xunfei OpenAI integration probe", xunfeiOpenAIRunEnv)
	}

	cfg := xunfeiOpenAIIntegrationConfig{
		BaseURL:    strings.TrimSpace(os.Getenv(xunfeiOpenAIBaseURLEnv)),
		APIKey:     strings.TrimSpace(os.Getenv(xunfeiOpenAIAPIKeyEnv)),
		Model:      strings.TrimSpace(os.Getenv(xunfeiOpenAIModelEnv)),
		Provider:   strings.TrimSpace(os.Getenv(xunfeiOpenAIProviderEnv)),
		Timeout:    30 * time.Second,
		MaxTokens:  512,
		Streamable: true,
	}

	if cfg.Provider == "" {
		cfg.Provider = "xunfei"
	}

	if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "" {
		t.Fatalf("missing required envs: %s, %s, %s", xunfeiOpenAIBaseURLEnv, xunfeiOpenAIAPIKeyEnv, xunfeiOpenAIModelEnv)
	}

	if raw := strings.TrimSpace(os.Getenv(xunfeiOpenAITimeoutEnv)); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			t.Fatalf("invalid %s: %q", xunfeiOpenAITimeoutEnv, raw)
		}
		cfg.Timeout = time.Duration(seconds) * time.Second
	}

	if raw := strings.TrimSpace(os.Getenv(xunfeiOpenAIMaxTokens)); raw != "" {
		maxTokens, err := strconv.Atoi(raw)
		if err != nil || maxTokens <= 0 {
			t.Fatalf("invalid %s: %q", xunfeiOpenAIMaxTokens, raw)
		}
		cfg.MaxTokens = maxTokens
	}

	return cfg
}

func buildResearchPlanStatisticsToolInfo() *schema.ToolInfo {
	return &schema.ToolInfo{
		Name: "get_research_plan_statistics",
		Desc: "查询科研系统中的方案统计数据并返回中文结果。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"question": {
				Type:     schema.String,
				Desc:     "用户提出的统计问题，例如：一级方案有多少。",
				Required: true,
			},
		}),
	}
}

func collectProbeResponses(ctx context.Context, ch chan *schema.Message) ([]*schema.Message, error) {
	var messages []*schema.Message

	for {
		select {
		case <-ctx.Done():
			return messages, ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return messages, nil
			}
			if msg == nil {
				continue
			}
			messages = append(messages, msg)
			if errText, ok := msg.Extra["error"].(string); ok && strings.TrimSpace(errText) != "" {
				return messages, fmt.Errorf("%s", errText)
			}
		}
	}
}

func logProbeResult(t *testing.T, messages []*schema.Message, err error) {
	t.Helper()

	for idx, msg := range messages {
		t.Logf("response[%d]: role=%s content=%q tool_calls=%d tool_call_id=%q extra=%v",
			idx,
			msg.Role,
			msg.Content,
			len(msg.ToolCalls),
			msg.ToolCallID,
			msg.Extra,
		)
	}

	if err != nil {
		t.Logf("probe error: %v", err)
	}
}
