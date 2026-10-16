package config

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Settings 是运行时配置。
type Settings struct {
	Proxy                      string `json:"proxy" yaml:"proxy"`
	WebHost                    string `json:"web_host" yaml:"web_host"`
	WebPort                    int    `json:"web_port" yaml:"-"`
	WebUCHost                  string `json:"-" yaml:"web_uc_host"`
	AdminPassword              string `json:"-" yaml:"admin_password"`
	StatusCheckIntervalSeconds int    `json:"status_check_interval_seconds" yaml:"status_check_seconds"`
	RetryCount                 int    `json:"retry_count" yaml:"retry_count"`
	ChatDelete                 bool   `json:"chat_delete" yaml:"delete_chat"`
	MaxChatHistoryLength       int    `json:"max_chat_history_length" yaml:"max_history_length"`
	RemoveInvalidAccount       bool   `json:"remove_invalid_account" yaml:"remove_invalid_account"`
	DetailedAPILog             bool   `json:"detailed_api_log" yaml:"detailed_api_log"`
	// QuotaBalanceGap 额度均衡阈值（0~1）：只在 5 小时剩余不低于「最大剩余 - 该值」的账号里轮流分配；0 关闭。
	QuotaBalanceGap float64 `json:"quota_balance_gap" yaml:"quota_balance_gap"`
	// ScheduleEnabled 定时检查账号开关（默认关）；ScheduleTime 为北京时间 "HH:MM"，非法时回退 "06:00"。
	ScheduleEnabled bool   `json:"schedule_enabled" yaml:"schedule_enabled"`
	ScheduleTime    string `json:"schedule_time" yaml:"schedule_time"`
	// ModelPrices 模型单价表（美元 / 百万 token），仅用于用量统计页的费用折算，订阅账号实际不按此计费。
	ModelPrices map[string]ModelPrice `json:"model_prices" yaml:"model_prices"`
}

// ModelPrice 是某个模型每百万 token 的输入 / 输出价格（美元）。
type ModelPrice struct {
	Input  float64 `json:"input" yaml:"input"`
	Output float64 `json:"output" yaml:"output"`
}

// DefaultModelPrices 返回 Anthropic 官方 API 默认价格（2026-10 核对，haiku-5-5 取提示词不超过 10 万 token 的价格）。
func DefaultModelPrices() map[string]ModelPrice {
	return map[string]ModelPrice{
		"claude-sonnet-5-5":          {Input: 2, Output: 10},
		"claude-sonnet-5":            {Input: 2, Output: 10},
		"claude-sonnet-4-6":          {Input: 3, Output: 15},
		"claude-haiku-5-5":           {Input: 0.10, Output: 0.50},
		"claude-haiku-4-5-20251001":  {Input: 1, Output: 5},
	}
}

// cleanPrices 丢弃空模型名和非法（负数 / NaN / Inf）价格。
func cleanPrices(in map[string]ModelPrice) map[string]ModelPrice {
	out := map[string]ModelPrice{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		bad := func(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) || f < 0 }
		if k == "" || bad(v.Input) || bad(v.Output) {
			continue
		}
		out[k] = v
	}
	return out
}

// parsePrices 把前端传来的 {模型: {input, output}} 解析成单价表；格式不对返回 false（调用方保持原值）。
func parsePrices(v any) (map[string]ModelPrice, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	num := func(x any) float64 {
		f, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(x)), 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	raw := map[string]ModelPrice{}
	for k, e := range m {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		raw[k] = ModelPrice{Input: num(em["input"]), Output: num(em["output"])}
	}
	return cleanPrices(raw), true
}

var (
	baseDir    string
	configPath string

	current   Settings
	currentMu sync.RWMutex
)

// Load 加载配置。
func Load() {
	exe, err := os.Executable()
	if err == nil {
		baseDir = filepath.Dir(exe)
	} else {
		baseDir, _ = os.Getwd()
	}
	// 开发运行时优先使用当前目录的配置。
	if wd, err := os.Getwd(); err == nil {
		_, e1 := os.Stat(filepath.Join(wd, "config.yaml"))
		_, e2 := os.Stat(filepath.Join(wd, "config.example.yaml"))
		if e1 == nil || e2 == nil {
			baseDir = wd
		}
	}
	configPath = filepath.Join(baseDir, "config.yaml")
	ensureConfig(configPath)
	current = Settings{WebHost: "127.0.0.1", WebPort: 8787, WebUCHost: "localhost", StatusCheckIntervalSeconds: 21600, ChatDelete: true, MaxChatHistoryLength: 12000, QuotaBalanceGap: 0.05, ScheduleTime: DefaultScheduleTime}
	data, _ := os.ReadFile(configPath)
	_ = yaml.Unmarshal(data, &current)
	current.Proxy = strings.TrimSpace(current.Proxy)
	current.AdminPassword = strings.TrimSpace(current.AdminPassword)
	current.WebHost = strings.TrimSpace(current.WebHost)
	if current.WebHost == "" {
		current.WebHost = "127.0.0.1"
	}
	current.WebUCHost = strings.TrimSpace(current.WebUCHost)
	if current.WebUCHost == "" {
		current.WebUCHost = "localhost"
	}
	if current.StatusCheckIntervalSeconds < 21600 {
		current.StatusCheckIntervalSeconds = 21600
	}
	if current.MaxChatHistoryLength <= 0 {
		current.MaxChatHistoryLength = 12000
	}
	current.QuotaBalanceGap = clampGap(current.QuotaBalanceGap)
	current.ScheduleTime = NormalizeScheduleTime(current.ScheduleTime)
	if len(current.ModelPrices) == 0 {
		current.ModelPrices = DefaultModelPrices()
	} else {
		current.ModelPrices = cleanPrices(current.ModelPrices)
	}
}

// clampGap 把均衡阈值夹到 [0,1]；NaN 视为 0（关闭）。
func clampGap(g float64) float64 {
	if math.IsNaN(g) || g < 0 {
		return 0
	}
	if g > 1 {
		return 1
	}
	return g
}

// DefaultScheduleTime 是定时检查账号的默认时间（北京时间）。
const DefaultScheduleTime = "06:00"

// NormalizeScheduleTime 校验 "HH:MM"（00:00~23:59）；非法返回默认值，合法则补零成两位格式。
func NormalizeScheduleTime(s string) string {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return DefaultScheduleTime
	}
	h, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return DefaultScheduleTime
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

// parseGap 解析阈值；无法解析或为 NaN/Inf 时返回 false（调用方保持原值）。
func parseGap(v any) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(v)), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return clampGap(f), true
}

func Get() Settings {
	currentMu.RLock()
	defer currentMu.RUnlock()
	return current
}

// ensureConfig 缺配置时复制示例文件。
func ensureConfig(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	example := filepath.Join(filepath.Dir(path), "config.example.yaml")
	data, err := os.ReadFile(example)
	if err != nil {
		slog.Warn("[配置] 未找到 config.yaml，也无 config.example.yaml 可复制，使用内置默认值", "err", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("[配置] 复制 config.example.yaml 失败，使用内置默认值", "err", err)
		return
	}
	slog.Info("[配置] 未找到 config.yaml，已从 config.example.yaml 复制生成", "path", path)
}

// DataDir 返回数据目录。
func DataDir() string {
	return filepath.Join(baseDir, "data")
}

// DBPath 返回数据库路径。
func DBPath() string {
	return filepath.Join(DataDir(), "app.db")
}

func writeConfig() {
	data, _ := yaml.Marshal(&current)
	_ = os.WriteFile(configPath, data, 0o644)
}

func Update(patch map[string]any) Settings {
	currentMu.Lock()
	defer currentMu.Unlock()
	for k, v := range patch {
		switch k {
		case "proxy":
			current.Proxy = strings.TrimSpace(fmt.Sprint(v))
		case "admin_password":
			current.AdminPassword = strings.TrimSpace(fmt.Sprint(v))
		case "status_check_interval_seconds":
			current.StatusCheckIntervalSeconds = mustAtoi(v)
		case "retry_count":
			current.RetryCount = mustAtoi(v)
		case "chat_delete":
			current.ChatDelete = mustBool(v)
		case "max_chat_history_length":
			current.MaxChatHistoryLength = mustAtoi(v)
		case "remove_invalid_account":
			current.RemoveInvalidAccount = mustBool(v)
		case "detailed_api_log":
			current.DetailedAPILog = mustBool(v)
		case "quota_balance_gap":
			// 空值/非法值保持原值，避免误清空把均衡悄悄关掉；要关闭请明确填 0。
			if g, ok := parseGap(v); ok {
				current.QuotaBalanceGap = g
			}
		case "schedule_enabled":
			current.ScheduleEnabled = mustBool(v)
		case "schedule_time":
			// 非法格式回退默认值，避免定时任务悄悄失效。
			current.ScheduleTime = NormalizeScheduleTime(fmt.Sprint(v))
		case "model_prices":
			// 格式不对保持原值；用户把表清空则恢复官方默认价。
			if p, ok := parsePrices(v); ok {
				if len(p) == 0 {
					p = DefaultModelPrices()
				}
				current.ModelPrices = p
			}
		}
	}
	writeConfig()
	return current
}

func mustAtoi(v any) int {
	n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(v)))
	return n
}

func mustBool(v any) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(fmt.Sprint(v)))
	return b
}
