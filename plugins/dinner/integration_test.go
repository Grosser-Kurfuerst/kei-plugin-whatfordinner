package dinner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomLemon/kei/pkg/bot"
	"github.com/RandomLemon/kei/pkg/kei"

	_ "github.com/RandomLemon/kei/adapters/mock"
)

// TestSQLitePersistenceAcrossRestart 使用真实 kei 装配、Mock 适配器和 SQLite，
// 验证投票与个人推荐在进程重启后仍然可查询。
func TestSQLitePersistenceAcrossRestart(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "dinner.db")
	addr := freeAddr(t)
	config := fmt.Sprintf(`
bots:
- name: mock-main
  adapter: mock
  listen_addr: %s
storage:
  type: sqlite
  dsn: %s
plugins:
  dinner:
    enabled: true
    catalog:
      items:
      - {id: a, name: A, price_cents: 1800, enabled: true}
      - {id: b, name: B, price_cents: 3000, enabled: true}
`, addr, dsn)

	base, stop := startBot(t, addr, config)
	integrationInject(t, base, "/dinner 18")
	integrationWaitSent(t, base, 1, "A [a] — 0 票")
	integrationInject(t, base, "/dinner 投票 a")
	integrationWaitSent(t, base, 2, "[a] — 1 票")
	integrationInject(t, base, "/dinner 推荐")
	integrationWaitSent(t, base, 3, "今晚推荐：A [a]")
	stop()

	base, stop = startBot(t, addr, config)
	defer stop()
	integrationInject(t, base, "/dinner")
	integrationWaitSent(t, base, 1, "[a] — 1 票")
	integrationInject(t, base, "/dinner 推荐")
	integrationWaitSent(t, base, 2, "今晚推荐：A [a]")
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func startBot(t *testing.T, addr, config string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- kei.Run(ctx, kei.Options{Config: []byte(config), Plugins: []bot.Plugin{&Plugin{}}})
	}()
	base := "http://" + addr
	integrationWaitHealthy(t, base)
	return base, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("kei.Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("kei.Run did not stop")
		}
	}
}

func integrationWaitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", base)
}

func integrationInject(t *testing.T, base, text string) {
	t.Helper()
	body := fmt.Sprintf(`{"text":%q,"user_id":"u1","kind":"group","channel_id":"g1"}`, text)
	resp, err := http.Post(base+"/inject", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("inject %q: %s", text, resp.Status)
	}
}

func integrationWaitSent(t *testing.T, base string, count int, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/sent")
		if err == nil {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			var records []json.RawMessage
			if readErr == nil && json.Unmarshal(data, &records) == nil && len(records) >= count && strings.Contains(string(data), want) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d replies containing %q", count, want)
}
