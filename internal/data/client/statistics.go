package client

import "time"

// Statistic 结构体已废弃，请使用 statistic_plugin 在 MetricTtsStop 时获取统计信息
type Statistic struct {
	TurnStartTs     int64
	AsrFirstTextTs  int64
	AsrFinalTextTs  int64
	LlmStartTs      int64
	LlmFirstTokenTs int64
	LlmEndTs        int64
	TtsStartTs      int64
	TtsFirstFrameTs int64
	TtsStopTs       int64
}

// MarkTurnStart 记录轮次开始时间
func (state *ClientState) MarkTurnStart() {
	state.Statistic.TurnStartTs = time.Now().UnixMilli()
}

// MarkAsrFirstText 记录 ASR 首次返回文本时间
func (state *ClientState) MarkAsrFirstText() {
	if state.Statistic.AsrFirstTextTs == 0 {
		state.Statistic.AsrFirstTextTs = time.Now().UnixMilli()
	}
}

// MarkAsrFinalText 记录 ASR 最终文本时间
func (state *ClientState) MarkAsrFinalText() {
	if state.Statistic.AsrFinalTextTs == 0 {
		state.Statistic.AsrFinalTextTs = time.Now().UnixMilli()
	}
}

// MarkLlmStart 记录 LLM 开始时间
func (state *ClientState) MarkLlmStart() {
	state.Statistic.LlmStartTs = time.Now().UnixMilli()
	state.Statistic.LlmFirstTokenTs = 0
	state.Statistic.LlmEndTs = 0
}

// MarkLlmFirstToken 记录 LLM 首次返回 token 时间
func (state *ClientState) MarkLlmFirstToken() {
	state.Statistic.LlmFirstTokenTs = time.Now().UnixMilli()
}

// MarkLlmEnd 记录 LLM 结束时间
func (state *ClientState) MarkLlmEnd() {
	state.Statistic.LlmEndTs = time.Now().UnixMilli()
}

// MarkTtsStart 记录 TTS 开始时间
func (state *ClientState) MarkTtsStart() {
	state.Statistic.TtsStartTs = time.Now().UnixMilli()
	state.Statistic.TtsFirstFrameTs = 0
	state.Statistic.TtsStopTs = 0
}

// MarkTtsFirstFrame 记录 TTS 首帧时间
func (state *ClientState) MarkTtsFirstFrame() {
	if state.Statistic.TtsFirstFrameTs == 0 {
		state.Statistic.TtsFirstFrameTs = time.Now().UnixMilli()
	}
}

// MarkTtsStop 记录 TTS 结束时间
func (state *ClientState) MarkTtsStop() {
	state.Statistic.TtsStopTs = time.Now().UnixMilli()
}

// SetStartAsrTs 设置 ASR 开始时间（别名，为了兼容）
func (state *ClientState) SetStartAsrTs() { state.MarkTurnStart() }

// SetStartLlmTs 设置 LLM 开始时间（别名，为了兼容）
func (state *ClientState) SetStartLlmTs() { state.MarkLlmStart() }

// SetStartTtsTs 设置 TTS 开始时间（别名，为了兼容）
func (state *ClientState) SetStartTtsTs() { state.MarkTtsStart() }

// GetAsrDuration 获取 ASR 处理耗时（已废弃，仅保留方法签名）
func (state *ClientState) GetAsrDuration() int64 {
	return 0
}

// GetAsrLlmTtsDuration 获取整体耗时（已废弃，仅保留方法签名）
func (state *ClientState) GetAsrLlmTtsDuration() int64 {
	return 0
}

// GetLlmDuration 获取 LLM 耗时（已废弃，仅保留方法签名）
func (state *ClientState) GetLlmDuration() int64 {
	return 0
}

// GetTtsDuration 获取 TTS 耗时（已废弃，仅保留方法签名）
func (state *ClientState) GetTtsDuration() int64 {
	return 0
}

func (s *Statistic) Reset() {}
