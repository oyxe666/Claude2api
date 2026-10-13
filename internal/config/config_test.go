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
