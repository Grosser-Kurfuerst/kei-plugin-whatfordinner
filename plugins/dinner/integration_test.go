package dinner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomLemon/kei/pkg/bot"
	"github.com/RandomLemon/kei/pkg/kei"
	_ "github.com/mattn/go-sqlite3"

	_ "github.com/RandomLemon/kei/adapters/mock"
)

// TestSQLitePersistenceAcrossRestart 使用真实 kei 装配、Mock 适配器和 SQLite，
// 验证投票与个人推荐在进程重启后仍然可查询。
func TestSQLitePersistenceAcrossRestart(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "dinner.db")
	seed := filepath.Join(t.TempDir(), "restaurants.yaml")
	if err := os.WriteFile(seed, []byte("items:\n- {id: a, name: A, price_cents: 1800, enabled: true}\n- {id: b, name: B, price_cents: 3000, enabled: true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
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
    tag_groups:
    - name: 大类
      tags: [中餐]
    - name: 菜系
      tags: [川菜]
    - name: 品类
      tags: [面食]
    - name: 就餐形式
      tags: [点餐]
    catalog_seed: %q
`, addr, dsn, seed)

	base, stop := startBot(t, addr, config)
	integrationInject(t, base, "/dinner 18")
	integrationWaitSent(t, base, 1, "A [a] — 0 票")
	integrationInject(t, base, "/dinner 投票 a")
	integrationWaitSent(t, base, 2, "[a] — 1 票")
	integrationInject(t, base, "/dinner 推荐")
	integrationWaitSent(t, base, 3, "今晚推荐：A [a]")
	integrationInject(t, base, "/dinner 已吃 a")
	integrationWaitSent(t, base, 4, "今天已吃并记录：A [a]")
	integrationInject(t, base, "/dinner 历史")
	integrationWaitSent(t, base, 5, "A [a]")
	stop()

	base, stop = startBot(t, addr, config)
	defer stop()
	integrationInject(t, base, "/dinner")
	integrationWaitSent(t, base, 1, "[a] — 1 票")
	integrationInject(t, base, "/dinner 历史")
	integrationWaitSent(t, base, 2, "A [a]")
	integrationInject(t, base, "/dinner 推荐")
	integrationWaitSent(t, base, 3, "今天已吃并记录：A [a]")
}

func TestCatalogStorageLifecycle(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "catalog.db")
	seed := filepath.Join(t.TempDir(), "restaurants.yaml")
	writeSeed := func(content string) {
		t.Helper()
		if err := os.WriteFile(seed, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSeed("items:\n- {id: a, name: A, enabled: true}\n")

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
    tag_groups:
    - name: 大类
      tags: [中餐]
    - name: 菜系
      tags: [川菜]
    - name: 品类
      tags: [面食]
    - name: 就餐形式
      tags: [点餐]
    catalog_seed: %q
`, addr, dsn, seed)

	base, stop := startBot(t, addr, config)
	integrationInject(t, base, "/dinner 候选")
	integrationWaitSent(t, base, 1, "A [a]")
	stop()
	if !catalogStored(t, dsn) {
		t.Fatal("first startup did not persist dinner:catalog")
	}

	writeSeed("items:\n- {id: b, name: B, enabled: true}\n")
	base, stop = startBot(t, addr, config)
	integrationInject(t, base, "/dinner 候选")
	integrationWaitSent(t, base, 1, "A [a]")
	if sent := integrationSent(t, base); strings.Contains(sent, "B [b]") {
		t.Fatal("restart reread the changed seed instead of stored catalog")
	}
	stop()

	deleteCatalogKey(t, dsn)
	base, stop = startBot(t, addr, config)
	integrationInject(t, base, "/dinner 候选")
	integrationWaitSent(t, base, 1, "B [b]")
	stop()
}

func catalogStored(t *testing.T, dsn string) bool {
	t.Helper()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value []byte
	err = db.QueryRow(`SELECT value FROM kei_storage WHERE key = ?`, "dinner:catalog").Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(value) > 0
}

func deleteCatalogKey(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.Exec(`DELETE FROM kei_storage WHERE key = ?`, "dinner:catalog")
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("deleted rows = %d, want 1", affected)
	}
}

func integrationSent(t *testing.T, base string) string {
	t.Helper()
	resp, err := http.Get(base + "/sent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
