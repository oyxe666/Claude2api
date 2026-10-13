package service

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"claude2api/internal/repository"
)

func setQuotaOK(email string) bool {
	return repository.UpdateAccountQuota(email, func(q *repository.AccountQuota) {
		q.Type = "within_limit"
		q.UpdatedAt = time.Now()
	})
}

func recv(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("2 秒内没有收到事件")
		return nil
	}
}

func TestEventsReceiveQuotaAndNoSecrets(t *testing.T) {
	setupDB(t)
	if err := repository.UpsertAccount(&repository.Account{
		Email: "ev@example.com", OrgUUID: "ORG-SECRET",
		Cookies: map[string]string{"sessionKey": "SECRET-SESSION"}, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	InstallQuotaBroadcast()
	t.Cleanup(func() { repository.OnQuotaChanged = nil })
	ch, cancel := SubscribeEvents()
	defer cancel()

	if !setQuotaOK("ev@example.com") {
		t.Fatal("更新额度失败")
	}
	raw := recv(t, ch)
	var ev QuotaEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Email != "ev@example.com" || ev.Account == nil || ev.Account.Quota == nil ||
		ev.Account.Quota.Type != "within_limit" || !ev.Account.HasSession {
		t.Fatalf("事件内容不对: %s", raw)
	}
	for _, bad := range []string{"SECRET-SESSION", "ORG-SECRET", "sessionKey", "Cookies", "org_uuid"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("事件泄漏了 %q: %s", bad, raw)
		}
	}
}

func TestEventsCancelStopsDelivery(t *testing.T) {
	setupDB(t)
	addAccount(t, "c@example.com", testOrg, "sk-test-c")
	InstallQuotaBroadcast()
	t.Cleanup(func() { repository.OnQuotaChanged = nil })
	base := EventSubscriberCount()
	ch, cancel := SubscribeEvents()
	if EventSubscriberCount() != base+1 {
		t.Fatal("订阅数应 +1")
	}
	cancel()
	cancel() // 可重复调用
	if EventSubscriberCount() != base {
		t.Fatal("取消后订阅数应恢复")
	}
	setQuotaOK("c@example.com")
	select {
	case b := <-ch:
		t.Fatalf("取消后不应再收到: %s", b)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEventsSlowSubscriberDoesNotBlock(t *testing.T) {
	setupDB(t)
	addAccount(t, "s@example.com", testOrg, "sk-test-s")
	_, cancel := SubscribeEvents() // 只订阅不读
	defer cancel()

	start := time.Now()
	for i := 0; i < 100; i++ {
		PublishQuotaChanged("s@example.com")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("100 次发布耗时 %v，被慢订阅者阻塞", d)
	}
}

func TestEventsNoSubscribersNoPanic(t *testing.T) {
	setupDB(t)
	PublishQuotaChanged("nobody@example.com")
	PublishQuotaChanged("")
}

func TestEventsConcurrent(t *testing.T) {
	setupDB(t)
	addAccount(t, "p@example.com", testOrg, "sk-test-p")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ch, cancel := SubscribeEvents()
			defer cancel()
			select {
			case <-ch:
			case <-time.After(20 * time.Millisecond):
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				PublishQuotaChanged("p@example.com")
			}
		}()
	}
	wg.Wait()
}

func TestEventAccountViewNil(t *testing.T) {
	if EventAccountView(nil) != nil {
		t.Fatal("nil 应返回 nil")
	}
}
