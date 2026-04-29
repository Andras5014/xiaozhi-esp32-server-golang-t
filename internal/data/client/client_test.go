package client

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestAlignToolMessagesPreservesCompleteMultiToolSegment(t *testing.T) {
	messages := []*schema.Message{
		schema.UserMessage("帮我安排今天的行程"),
		schema.AssistantMessage("", []schema.ToolCall{
			{
				ID:   "call_calendar_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "self_calendar_get_events",
					Arguments: `{"date":"2026-04-29"}`,
				},
			},
			{
				ID:   "call_weather_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "self_weather_get_forecast",
					Arguments: `{"city":"Shanghai"}`,
				},
			},
		}),
		schema.ToolMessage(`{"events":[]}`, "call_calendar_1"),
		schema.ToolMessage(`{"weather":"sunny"}`, "call_weather_1"),
		schema.AssistantMessage("今天上午没有安排，天气晴朗。", nil),
	}

	aligned := AlignToolMessages(messages)
	if len(aligned) != 5 {
		t.Fatalf("expected 5 messages after alignment, got %d", len(aligned))
	}
	if aligned[1].Role != schema.Assistant {
		t.Fatalf("expected assistant tool-call message at index 1, got %s", aligned[1].Role)
	}
	if len(aligned[1].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(aligned[1].ToolCalls))
	}
	if aligned[2].Role != schema.Tool || aligned[2].ToolCallID != "call_calendar_1" {
		t.Fatalf("expected first tool result to follow assistant, got %+v", aligned[2])
	}
	if aligned[3].Role != schema.Tool || aligned[3].ToolCallID != "call_weather_1" {
		t.Fatalf("expected second tool result to follow assistant, got %+v", aligned[3])
	}
}

func TestAlignToolMessagesDropsIncompleteToolCallSegment(t *testing.T) {
	messages := []*schema.Message{
		schema.UserMessage("帮我安排今天的行程"),
		schema.AssistantMessage("", []schema.ToolCall{
			{
				ID:   "call_calendar_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "self_calendar_get_events",
					Arguments: `{"date":"2026-04-29"}`,
				},
			},
			{
				ID:   "call_weather_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "self_weather_get_forecast",
					Arguments: `{"city":"Shanghai"}`,
				},
			},
		}),
		schema.ToolMessage(`{"events":[]}`, "call_calendar_1"),
		schema.AssistantMessage("今天上午没有安排。", nil),
	}

	aligned := AlignToolMessages(messages)
	if len(aligned) != 2 {
		t.Fatalf("expected incomplete tool-call segment to be dropped, got %d messages", len(aligned))
	}
	if aligned[0].Role != schema.User {
		t.Fatalf("expected user message to remain, got %s", aligned[0].Role)
	}
	if aligned[1].Role != schema.Assistant || aligned[1].Content != "今天上午没有安排。" {
		t.Fatalf("expected trailing assistant text message to remain, got %+v", aligned[1])
	}
}
