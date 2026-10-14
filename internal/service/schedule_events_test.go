package service

import (
	"testing"
	"time"
)

func TestPublishScheduleChanged(t *testing.T) {
	ch, cancel := SubscribeScheduleEvents()
	PublishScheduleChanged()
	PublishScheduleChanged() // 合并，不阻塞
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("没收到定时检查通知")
	}
	cancel()
	cancel()
	PublishScheduleChanged() // 取消后不 panic
}
