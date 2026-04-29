package xunfei_openai_compat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	extopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	runEnv        = "XUNFEI_OPENAI_RUN"
	baseURLEnv    = "XUNFEI_OPENAI_BASE_URL"
	apiKeyEnv     = "XUNFEI_OPENAI_API_KEY"
	modelEnv      = "XUNFEI_OPENAI_MODEL"
	timeoutEnv    = "XUNFEI_OPENAI_TIMEOUT_SECONDS"
	maxTokensEnv  = "XUNFEI_OPENAI_MAX_TOKENS"
	payloadMaxLen = 32 * 1024
)

func TestXunfeiOpenAICompatProbeStandalone(t *testing.T) {
	cfg := loadStandaloneProbeConfig(t)

	httpClient := &http.Client{
		Transport: &probeLoggingRoundTripper{
			base: http.DefaultTransport,
			logf: t.Logf,
		},
		Timeout: 0,
	}

	chatModel, err := extopenai.NewChatModel(context.Background(), &extopenai.ChatModelConfig{
		Model:      cfg.Model,
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		HTTPClient: httpClient,
		MaxTokens:  intPtr(cfg.MaxTokens),
	})
	if err != nil {
		t.Fatalf("create chat model: %v", err)
	}

	withTools, err := chatModel.WithTools([]*schema.ToolInfo{buildResearchPlanStatisticsToolInfo()})
	if err != nil {
		t.Fatalf("bind tools: %v", err)
	}
	complexTools := buildComplexToolCatalog()
	withComplexTools, err := chatModel.WithTools(complexTools)
	if err != nil {
		t.Fatalf("bind complex tools: %v", err)
	}

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

		parts, err := collectStandaloneStream(ctx, t, chatModel, messages)
		if err != nil {
			t.Fatalf("plain_text failed: %v", err)
		}
		t.Logf("plain_text content=%q", strings.Join(parts, ""))
	})

	t.Run("tool_first_round", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		messages := []*schema.Message{
			schema.SystemMessage("你是一个中文助手。能用工具时优先调用工具。"),
			schema.UserMessage("一级方案有多少"),
		}

		parts, err := collectStandaloneStream(ctx, t, withTools, messages)
		t.Logf("tool_first_round deltas=%q", strings.Join(parts, ""))
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

		parts, err := collectStandaloneStream(ctx, t, withTools, messages)
		t.Logf("tool_followup_with_tools deltas=%q", strings.Join(parts, ""))
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

		parts, err := collectStandaloneStream(ctx, t, chatModel, messages)
		t.Logf("tool_followup_without_tools deltas=%q", strings.Join(parts, ""))
		if err != nil {
			t.Fatalf("tool_followup_without_tools failed: %v", err)
		}
	})

	t.Run("complex_tools_first_round", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		messages := []*schema.Message{
			schema.SystemMessage("你是一个中文助手。能用工具时优先调用工具。"),
			schema.UserMessage("一级方案有多少"),
		}

		parts, err := collectStandaloneStream(ctx, t, withComplexTools, messages)
		t.Logf("complex_tools_first_round deltas=%q", strings.Join(parts, ""))
		if err != nil {
			t.Fatalf("complex_tools_first_round failed: %v", err)
		}
	})

	t.Run("complex_tools_followup_with_tools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		defer cancel()

		messages := []*schema.Message{
			schema.SystemMessage("你是一个中文助手。请根据工具结果直接回答用户。"),
			schema.UserMessage("一级方案有多少"),
			schema.AssistantMessage("", []schema.ToolCall{toolCall}),
			schema.ToolMessage(toolResult, toolCallID),
		}

		parts, err := collectStandaloneStream(ctx, t, withComplexTools, messages)
		t.Logf("complex_tools_followup_with_tools deltas=%q", strings.Join(parts, ""))
		if err != nil {
			t.Fatalf("complex_tools_followup_with_tools failed: %v", err)
		}
	})
}

type standaloneProbeConfig struct {
	BaseURL   string
	APIKey    string
	Model     string
	Timeout   time.Duration
	MaxTokens int
}

func loadStandaloneProbeConfig(t *testing.T) standaloneProbeConfig {
	t.Helper()

	if strings.TrimSpace(os.Getenv(runEnv)) == "" {
		t.Skipf("set %s=1 to run standalone Xunfei OpenAI compat probe", runEnv)
	}

	cfg := standaloneProbeConfig{
		BaseURL:   strings.TrimSpace(os.Getenv(baseURLEnv)),
		APIKey:    strings.TrimSpace(os.Getenv(apiKeyEnv)),
		Model:     strings.TrimSpace(os.Getenv(modelEnv)),
		Timeout:   30 * time.Second,
		MaxTokens: 512,
	}

	if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "" {
		t.Fatalf("missing required envs: %s, %s, %s", baseURLEnv, apiKeyEnv, modelEnv)
	}

	if raw := strings.TrimSpace(os.Getenv(timeoutEnv)); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			t.Fatalf("invalid %s: %q", timeoutEnv, raw)
		}
		cfg.Timeout = time.Duration(seconds) * time.Second
	}

	if raw := strings.TrimSpace(os.Getenv(maxTokensEnv)); raw != "" {
		maxTokens, err := strconv.Atoi(raw)
		if err != nil || maxTokens <= 0 {
			t.Fatalf("invalid %s: %q", maxTokensEnv, raw)
		}
		cfg.MaxTokens = maxTokens
	}

	return cfg
}

type probeLoggingRoundTripper struct {
	base http.RoundTripper
	logf func(format string, args ...any)
}

func (p *probeLoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.base == nil {
		p.base = http.DefaultTransport
	}

	var bodyBytes []byte
	if req != nil && req.Body != nil {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		bodyBytes = data
		req = cloneProbeRequest(req, data)
	}

	if len(bodyBytes) > 0 {
		p.logf("[Standalone-Probe] request method=%s url=%s body=%s",
			req.Method, req.URL.String(), truncateProbeText(string(bodyBytes), payloadMaxLen))
	}

	resp, err := p.base.RoundTrip(req)
	if err != nil {
		p.logf("[Standalone-Probe] transport err=%v", err)
		return nil, err
	}

	if resp != nil && resp.Body != nil && resp.StatusCode >= http.StatusBadRequest {
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		if readErr != nil {
			p.logf("[Standalone-Probe] response status=%d body_read_err=%v", resp.StatusCode, readErr)
		} else {
			p.logf("[Standalone-Probe] response status=%d body=%s", resp.StatusCode, truncateProbeText(string(respBody), payloadMaxLen))
		}
	}

	return resp, nil
}

func cloneProbeRequest(req *http.Request, body []byte) *http.Request {
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

func collectStandaloneStream(ctx context.Context, t *testing.T, chatModel model.ToolCallingChatModel, messages []*schema.Message) ([]string, error) {
	t.Helper()

	streamReader, err := chatModel.Stream(ctx, messages)
	if err != nil {
		return nil, err
	}
	defer streamReader.Close()

	var deltas []string
	for {
		msg, recvErr := streamReader.Recv()
		if recvErr == io.EOF {
			return deltas, nil
		}
		if recvErr != nil {
			return deltas, recvErr
		}
		if msg == nil {
			continue
		}

		if len(msg.ToolCalls) > 0 {
			payload, _ := json.Marshal(msg.ToolCalls)
			t.Logf("tool_calls=%s", payload)
		}
		if msg.Content != "" {
			deltas = append(deltas, msg.Content)
		}
	}
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

func buildComplexToolCatalog() []*schema.ToolInfo {
	names := []string{
		"self.calendar.get_categories",
		"self.bazi.analyze_marriage_timing",
		"self.bazi.analyze_marriage_compatibility",
		"exit_conversation",
		"self.bazi.get_solar_times",
		"self.application.kill",
		"music_player.seek",
		"control_music_playback",
		"timer.get_active_timers",
		"music_player.search_and_play",
		"music_player.get_local_playlist",
		"self.bazi.get_chinese_calendar",
		"take_photo",
		"self.bazi.build_bazi_from_solar_datetime",
		"self.application.list_running",
		"self.calendar.get_upcoming_events",
		"music_player.pause",
		"switch_device_role",
		"music_player.get_lyrics",
		"self.bazi.build_bazi_from_lunar_datetime",
		"self.calendar.delete_event",
		"self.calendar.get_events",
		"self.audio_speaker.get_volume",
		"restore_device_default_role",
		"self.application.launch",
		"take_screenshot",
		"self.application.scan_installed",
		"self.calendar.update_event",
		"clear_conversation_history",
		"self.calendar.delete_events_batch",
		"timer.start_countdown",
		"timer.cancel_countdown",
		"music_player.resume",
		"self.calendar.create_event",
		"music_player.stop",
		"self.bazi.get_bazi_detail",
		"self.audio_speaker.set_volume",
		"get_research_plan_statistics",
	}

	tools := make([]*schema.ToolInfo, 0, len(names))
	for _, name := range names {
		switch name {
		case "get_research_plan_statistics":
			tools = append(tools, buildResearchPlanStatisticsToolInfo())
		default:
			tools = append(tools, buildComplexToolInfo(name))
		}
	}
	return tools
}

func buildComplexToolInfo(name string) *schema.ToolInfo {
	params := map[string]*schema.ParameterInfo{}

	switch {
	case strings.Contains(name, "calendar.create_event"), strings.Contains(name, "calendar.update_event"):
		params = map[string]*schema.ParameterInfo{
			"title":      {Type: schema.String, Desc: "日程标题", Required: true},
			"start_time": {Type: schema.String, Desc: "开始时间，ISO8601", Required: true},
			"end_time":   {Type: schema.String, Desc: "结束时间，ISO8601"},
			"category":   {Type: schema.String, Desc: "分类名称"},
			"reminders": {
				Type: schema.Array,
				ElemInfo: &schema.ParameterInfo{
					Type: schema.Object,
					SubParams: map[string]*schema.ParameterInfo{
						"minutes_before": {Type: schema.Integer, Desc: "提前多少分钟", Required: true},
						"channel":        {Type: schema.String, Desc: "提醒渠道", Enum: []string{"app", "sms", "email"}},
					},
				},
			},
		}
	case strings.Contains(name, "calendar.delete_events_batch"):
		params = map[string]*schema.ParameterInfo{
			"event_ids": {
				Type:     schema.Array,
				Required: true,
				ElemInfo: &schema.ParameterInfo{Type: schema.String, Desc: "事件ID"},
			},
			"delete_series": {Type: schema.Boolean, Desc: "是否删除整个系列"},
		}
	case strings.Contains(name, "calendar.get_events"), strings.Contains(name, "calendar.get_upcoming_events"):
		params = map[string]*schema.ParameterInfo{
			"start_date": {Type: schema.String, Desc: "开始日期"},
			"end_date":   {Type: schema.String, Desc: "结束日期"},
			"category":   {Type: schema.String, Desc: "分类"},
			"limit":      {Type: schema.Integer, Desc: "返回数量上限"},
		}
	case strings.Contains(name, "calendar.delete_event"):
		params = map[string]*schema.ParameterInfo{
			"event_id": {Type: schema.String, Desc: "事件ID", Required: true},
		}
	case strings.Contains(name, "application.launch"), strings.Contains(name, "application.kill"):
		params = map[string]*schema.ParameterInfo{
			"app_name": {Type: schema.String, Desc: "应用名称", Required: true},
			"args": {
				Type: schema.Array,
				ElemInfo: &schema.ParameterInfo{
					Type: schema.String,
					Desc: "附加参数",
				},
			},
			"force": {Type: schema.Boolean, Desc: "是否强制执行"},
		}
	case strings.Contains(name, "application.scan_installed"), strings.Contains(name, "application.list_running"):
		params = map[string]*schema.ParameterInfo{
			"keyword": {Type: schema.String, Desc: "过滤关键字"},
			"limit":   {Type: schema.Integer, Desc: "返回数量上限"},
		}
	case strings.Contains(name, "audio_speaker.set_volume"):
		params = map[string]*schema.ParameterInfo{
			"volume": {Type: schema.Integer, Desc: "音量值 0-100", Required: true},
		}
	case strings.Contains(name, "audio_speaker.get_volume"), strings.Contains(name, "music_player.pause"), strings.Contains(name, "music_player.resume"), strings.Contains(name, "music_player.stop"), strings.Contains(name, "take_photo"), strings.Contains(name, "take_screenshot"), strings.Contains(name, "clear_conversation_history"), strings.Contains(name, "exit_conversation"), strings.Contains(name, "restore_device_default_role"), strings.Contains(name, "switch_device_role"), strings.Contains(name, "timer.get_active_timers"), strings.Contains(name, "calendar.get_categories"), strings.Contains(name, "music_player.get_local_playlist"), strings.Contains(name, "music_player.get_lyrics"):
		params = map[string]*schema.ParameterInfo{}
	case strings.Contains(name, "music_player.seek"):
		params = map[string]*schema.ParameterInfo{
			"position_seconds": {Type: schema.Integer, Desc: "跳转到第几秒", Required: true},
		}
	case strings.Contains(name, "music_player.search_and_play"):
		params = map[string]*schema.ParameterInfo{
			"query":  {Type: schema.String, Desc: "歌曲名称或歌手", Required: true},
			"source": {Type: schema.String, Desc: "音源", Enum: []string{"auto", "local", "online"}},
		}
	case strings.Contains(name, "control_music_playback"):
		params = map[string]*schema.ParameterInfo{
			"action": {Type: schema.String, Desc: "播放动作", Required: true, Enum: []string{"play", "pause", "resume", "stop", "next", "prev"}},
		}
	case strings.Contains(name, "timer.start_countdown"):
		params = map[string]*schema.ParameterInfo{
			"duration_seconds": {Type: schema.Integer, Desc: "倒计时秒数", Required: true},
			"label":            {Type: schema.String, Desc: "倒计时标签"},
		}
	case strings.Contains(name, "timer.cancel_countdown"):
		params = map[string]*schema.ParameterInfo{
			"timer_id": {Type: schema.String, Desc: "倒计时ID"},
			"label":    {Type: schema.String, Desc: "倒计时标签"},
		}
	case strings.Contains(name, "bazi.build_bazi_from_solar_datetime"), strings.Contains(name, "bazi.build_bazi_from_lunar_datetime"), strings.Contains(name, "bazi.get_bazi_detail"):
		params = map[string]*schema.ParameterInfo{
			"solar_datetime": {Type: schema.String, Desc: "公历出生时间"},
			"lunar_datetime": {Type: schema.String, Desc: "农历出生时间"},
			"gender":         {Type: schema.String, Desc: "性别", Enum: []string{"male", "female"}},
		}
	case strings.Contains(name, "bazi.analyze_marriage_timing"), strings.Contains(name, "bazi.analyze_marriage_compatibility"):
		params = map[string]*schema.ParameterInfo{
			"male_solar_datetime":   {Type: schema.String, Desc: "男方公历时间"},
			"male_lunar_datetime":   {Type: schema.String, Desc: "男方农历时间"},
			"female_solar_datetime": {Type: schema.String, Desc: "女方公历时间"},
			"female_lunar_datetime": {Type: schema.String, Desc: "女方农历时间"},
		}
	case strings.Contains(name, "bazi.get_solar_times"), strings.Contains(name, "bazi.get_chinese_calendar"):
		params = map[string]*schema.ParameterInfo{
			"date":     {Type: schema.String, Desc: "日期"},
			"location": {Type: schema.String, Desc: "地点"},
		}
	default:
		params = map[string]*schema.ParameterInfo{
			"query": {Type: schema.String, Desc: "通用查询参数"},
			"limit": {Type: schema.Integer, Desc: "返回数量上限"},
		}
	}

	return &schema.ToolInfo{
		Name:        name,
		Desc:        "复杂工具探针：" + name,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}
}

func intPtr(v int) *int {
	return &v
}

func truncateProbeText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}
