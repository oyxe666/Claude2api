package service

import (
	"context"
	"math/rand/v2"
	"time"

	"claude2api/internal/repository"
)

// 「检查账号」：逐个向 Claude 发一轮请求（预热 + /usage，状态与额度合并成一次），账号间隔随机。

// CheckGapMin / CheckGapMax 是检查账号时两个账号之间的随机间隔范围（测试里可改成 0）。
var CheckGapMin, CheckGapMax = 3 * time.Second, 8 * time.Second

// DefaultCheckBatchOptions 返回检查账号的批量配置：间隔在 [CheckGapMin, CheckGapMax] 随机，
// 不设总超时（账号多时总耗时很长，靠客户端断开来停止）。
func DefaultCheckBatchOptions() QuotaBatchOptions {
	return QuotaBatchOptions{
		Gap: func() time.Duration {
			lo, hi := CheckGapMin, CheckGapMax
			if hi <= lo {
				if lo < 0 {
					return 0
				}
				return lo
			}
			return lo + time.Duration(rand.Int64N(int64(hi-lo)+1))
		},
		Timeout: 0,
		Check:   CheckAccount,
	}
}

// CheckAccount 检查单个账号：只发一轮请求（RefreshAccountQuota），成功时把 error 状态置回 active。
func CheckAccount(ctx context.Context, email string) QuotaRefreshResult {
	res := RefreshAccountQuota(ctx, email)
	if res.OK {
		repository.UpdateAccount(email, func(a *repository.Account) {
			if a.Status == "error" {
				a.Status = "active"
			}
		})
	}
	return res
}
