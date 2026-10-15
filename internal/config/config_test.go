package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// saveCurrent 保存并在测试结束时还原包级配置（configPath 为空时 writeConfig 不会落盘到真实位置）。
func saveCurrent(t *testing.T) {
	t.Helper()
	currentMu.Lock()
	old, oldPath := current, configPath
	configPath = ""
	currentMu.Unlock()
	t.Cleanup(func() {
		currentMu.Lock()
		current, configPath = old, oldPath
		currentMu.Unlock()
	})
}

func TestUpdateQuotaBalanceGap(t *testing.T) {
	saveCurrent(t)
	Update(map[string]any{"quota_balance_gap": 0.3})

	cases := []struct {
		in   any
		want float64
	}{
		{"0.45", 0.45},
		{0.2, 0.2},
		{"0", 0},
		{"-1", 0},
		{"5", 1},
	}
	for _, c := range cases {
		if got := Update(map[string]any{"quota_balance_gap": c.in}).QuotaBalanceGap; got != c.want {
			t.Errorf("输入 %v: got %v want %v", c.in, got, c.want)
		}
	}

	// 非法/空值保持原值
	Update(map[string]any{"quota_balance_gap": "0.5"})
	for _, bad := range []any{"abc", "", "NaN", "Inf", nil} {
		if got := Update(map[string]any{"quota_balance_gap": bad}).QuotaBalanceGap; got != 0.5 {
			t.Errorf("非法输入 %v 应保持原值 0.5，got %v", bad, got)
		}
	}
}

func TestSettingsJSONHasQuotaBalanceGap(t *testing.T) {
	b, err := json.Marshal(Settings{QuotaBalanceGap: 0.3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"quota_balance_gap":0.3`) {
		t.Fatalf("JSON 缺少 quota_balance_gap: %s", b)
	}
}

func TestModelPrices(t *testing.T) {
	saveCurrent(t)
	Update(map[string]any{"model_prices": map[string]any{
		"m1":  map[string]any{"input": "1.5", "output": 7.5},
		"bad": map[string]any{"input": "-1", "output": 2},
		"nan": map[string]any{"input": "abc", "output": 2},
		" ":   map[string]any{"input": 1, "output": 2},
	}})
	got := Get().ModelPrices
	if len(got) != 1 || got["m1"].Input != 1.5 || got["m1"].Output != 7.5 {
		t.Fatalf("非法项应被丢弃: %+v", got)
	}
	// 格式不对保持原值
	Update(map[string]any{"model_prices": "oops"})
	if len(Get().ModelPrices) != 1 {
		t.Fatalf("非法格式应保持原值: %+v", Get().ModelPrices)
	}
	// 清空恢复默认
	Update(map[string]any{"model_prices": map[string]any{}})
	if p := Get().ModelPrices["claude-sonnet-5-5"]; p.Input != 2 || p.Output != 10 {
		t.Fatalf("清空应恢复官方默认价: %+v", Get().ModelPrices)
	}
}
