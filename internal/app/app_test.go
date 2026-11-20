package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/store/storetest"
)

// End to end: migrate a fresh database, start workers and HTTP, wait until
// the heartbeat job has gone through River, then shut down cleanly.
func TestServeUntilHealthy(t *testing.T) {
	url := storetest.EmptyDB(t)
	cfg := config.Config{DatabaseURL: url, DBMaxConns: 5, BlobDir: t.TempDir(), WatchDays: 60, FetchMinInterval: 3 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, storetest.Logger(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, ln) }()

	base := "http://" + ln.Addr().String()
	deadline := time.Now().Add(30 * time.Second)
	var last api.Health
	for {
		resp, err := http.Get(base + api.Prefix + "/healthz")
		if err == nil {
			last = api.Health{}
			_ = json.NewDecoder(resp.Body).Decode(&last)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && last.Status == "ok" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("not healthy after 30s: %+v (err %v)", last, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if last.LastHeartbeat == nil || last.Database != "ok" {
		t.Fatalf("health = %+v", last)
	}

	// Unknown API paths are a JSON 404; other paths belong to the web app
	// (503 when this binary was built without it).
	resp, err := http.Get(base + "/api/v1/no-such-operation")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown API path: %d", resp.StatusCode)
	}
	resp, err = http.Get(base + "/watches/some-id")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("web app path: %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusOK && !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-src") {
		t.Errorf("web app page without its CSP: %v", resp.Header)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(ShutdownTimeout + 10*time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
