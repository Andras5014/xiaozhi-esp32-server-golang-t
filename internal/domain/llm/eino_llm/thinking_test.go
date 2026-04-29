package eino_llm

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReasoningDetectReadCloserCollectsStreamedReasoningContent(t *testing.T) {
	tracker := &reasoningContentTracker{}
	reader := &reasoningDetectReadCloser{
		ReadCloser: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"choices":[{"delta":{"reasoning_content":"先分析"}}]}`,
			`data: {"choices":[{"delta":{"reasoning_content":"，再回答"}}]}`,
			`data: [DONE]`,
			"",
		}, "\n"))),
		tracker: tracker,
	}

	_, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.True(t, tracker.HasReturned())
	require.Equal(t, "先分析，再回答", tracker.Content())
}

func TestInjectStoredReasoningContentAddsReasoningToAssistantToolCallMessage(t *testing.T) {
	store := newReasoningSessionStore()
	store.StoreToolCallReasoning("session-1", []string{"call_search_1"}, "先分析，再回答")

	payload := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "system",
				"content": "你是助手",
			},
			map[string]interface{}{
				"role":    "assistant",
				"content": "",
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_search_1",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "search",
							"arguments": `{"question":"TJ5主线一般路基 2031a 数量"}`,
						},
					},
				},
			},
			map[string]interface{}{
				"role":         "tool",
				"tool_call_id": "call_search_1",
				"content":      `{"matched":false}`,
			},
		},
	}

	changed := injectStoredReasoningContent(payload, "session-1", store)
	require.True(t, changed)

	messages := payload["messages"].([]interface{})
	assistantMessage := messages[1].(map[string]interface{})
	require.Equal(t, "先分析，再回答", assistantMessage[reasoningContentExtraKey])
}

func TestInjectStoredReasoningContentSkipsUnknownToolCall(t *testing.T) {
	store := newReasoningSessionStore()
	store.StoreToolCallReasoning("session-1", []string{"call_search_1"}, "先分析，再回答")

	payload := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "assistant",
				"content": "",
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_search_2",
						"type": "function",
					},
				},
			},
		},
	}

	changed := injectStoredReasoningContent(payload, "session-1", store)
	require.False(t, changed)
}
