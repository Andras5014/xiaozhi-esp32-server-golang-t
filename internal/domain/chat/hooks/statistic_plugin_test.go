package hooks

import (
	"context"
	"testing"
)

func testHookContext(sessionID string) Context {
	return Context{
		Ctx:       context.Background(),
		SessionID: sessionID,
		DeviceID:  "device-test",
	}
}

func TestStatisticPluginHandlesLateTurnStart(t *testing.T) {
	plugin := newStatisticPlugin()
	ctx := testHookContext("session-late-start")

	plugin.onMetric(ctx, MetricData{Stage: MetricAsrFirstText, Ts: 20})
	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 10})

	tm := plugin.current[ctx.SessionID]
	if tm == nil {
		t.Fatalf("expected active turn for session %q", ctx.SessionID)
	}
	if tm.turnID != 1 {
		t.Fatalf("turnID = %d, want 1", tm.turnID)
	}
	if tm.turnStartTs != 10 {
		t.Fatalf("turnStartTs = %d, want 10", tm.turnStartTs)
	}
	if tm.asrFirstTextTs != 20 {
		t.Fatalf("asrFirstTextTs = %d, want 20", tm.asrFirstTextTs)
	}
}

func TestStatisticPluginCompletesOnTurnEndAndIgnoresLateMetrics(t *testing.T) {
	plugin := newStatisticPlugin()
	ctx := testHookContext("session-stop")

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 10})
	plugin.onMetric(ctx, MetricData{Stage: MetricLlmStart, Ts: 20})
	plugin.onMetric(ctx, MetricData{Stage: MetricTtsStart, Ts: 30})
	plugin.onMetric(ctx, MetricData{Stage: MetricTtsStop, Ts: 40})
	plugin.onMetric(ctx, MetricData{Stage: MetricTurnEnd, Ts: 50})

	if tm := plugin.current[ctx.SessionID]; tm != nil {
		t.Fatalf("expected current turn to be cleared after turn_end")
	}

	plugin.onMetric(ctx, MetricData{Stage: MetricLlmEnd, Ts: 60})
	plugin.onMetric(ctx, MetricData{Stage: MetricTtsStop, Ts: 70})

	if tm := plugin.current[ctx.SessionID]; tm != nil {
		t.Fatalf("late metrics should not create a new turn")
	}
}

func TestStatisticPluginCompletesTurnWithoutTts(t *testing.T) {
	plugin := newStatisticPlugin()
	ctx := testHookContext("session-no-tts")

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 10})
	plugin.onMetric(ctx, MetricData{Stage: MetricLlmStart, Ts: 20})
	plugin.onMetric(ctx, MetricData{Stage: MetricLlmEnd, Ts: 30})
	plugin.onMetric(ctx, MetricData{Stage: MetricTurnEnd, Ts: 35})

	if tm := plugin.current[ctx.SessionID]; tm != nil {
		t.Fatalf("expected current turn to be cleared after turn_end without tts")
	}
}

func TestStatisticPluginKeepsOnlyLatestTurn(t *testing.T) {
	plugin := newStatisticPlugin()
	ctx := testHookContext("session-latest-only")

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 10})
	plugin.onMetric(ctx, MetricData{Stage: MetricLlmStart, Ts: 20})
	plugin.onMetric(ctx, MetricData{Stage: MetricTtsStart, Ts: 30})

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 40})

	tm := plugin.current[ctx.SessionID]
	if tm == nil {
		t.Fatalf("expected latest turn to exist")
	}
	if tm.turnID != 2 {
		t.Fatalf("turnID = %d, want 2", tm.turnID)
	}
	if tm.turnStartTs != 40 {
		t.Fatalf("turnStartTs = %d, want 40", tm.turnStartTs)
	}
	if tm.llmStartTs != 0 {
		t.Fatalf("llmStartTs = %d, want 0", tm.llmStartTs)
	}
	if tm.ttsStartTs != 0 {
		t.Fatalf("ttsStartTs = %d, want 0", tm.ttsStartTs)
	}
}

func TestStatisticPluginDoesNotCrossOldTtsStopIntoNewTurn(t *testing.T) {
	plugin := newStatisticPlugin()
	ctx := testHookContext("session-no-cross")

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 10})
	plugin.onMetric(ctx, MetricData{Stage: MetricTtsStart, Ts: 20})

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 30})
	plugin.onMetric(ctx, MetricData{Stage: MetricTtsStop, Ts: 40})

	tm := plugin.current[ctx.SessionID]
	if tm == nil {
		t.Fatalf("expected latest turn to remain active")
	}
	if tm.turnID != 2 {
		t.Fatalf("turnID = %d, want 2", tm.turnID)
	}
	if tm.ttsStopTs != 0 {
		t.Fatalf("ttsStopTs = %d, want 0", tm.ttsStopTs)
	}
}

func TestStatisticPluginDoesNotCrossOldTurnEndIntoNewTurn(t *testing.T) {
	plugin := newStatisticPlugin()
	ctx := testHookContext("session-no-cross-turn-end")

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 10})
	plugin.onMetric(ctx, MetricData{Stage: MetricLlmStart, Ts: 20})

	plugin.onMetric(ctx, MetricData{Stage: MetricTurnStart, Ts: 30})
	plugin.onMetric(ctx, MetricData{Stage: MetricTurnEnd, Ts: 25})

	tm := plugin.current[ctx.SessionID]
	if tm == nil {
		t.Fatalf("expected latest turn to remain active")
	}
	if tm.turnID != 2 {
		t.Fatalf("turnID = %d, want 2", tm.turnID)
	}
	if tm.turnEndTs != 0 {
		t.Fatalf("turnEndTs = %d, want 0", tm.turnEndTs)
	}
}
