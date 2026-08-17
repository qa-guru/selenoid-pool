package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type cdStub struct {
	mu           sync.Mutex
	ids          []string
	creates      int
	navigations  []string
	deletes      int
	urlBySession map[string]string
}

func (c *cdStub) handler() http.Handler {
	if c.urlBySession == nil {
		c.urlBySession = map[string]string{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && path == "/sessions":
			items := make([]map[string]string, 0, len(c.ids))
			for _, id := range c.ids {
				items = append(items, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"value": items})
		case r.Method == http.MethodPost && path == "/session":
			c.creates++
			id := fmt.Sprintf("sess-%d", c.creates)
			c.ids = append(c.ids, id)
			c.urlBySession[id] = "about:blank"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": map[string]any{"sessionId": id},
			})
		case strings.HasPrefix(path, "/session/"):
			rest := strings.TrimPrefix(path, "/session/")
			parts := strings.Split(rest, "/")
			id := parts[0]
			tail := ""
			if len(parts) > 1 {
				tail = strings.Join(parts[1:], "/")
			}
			alive := false
			for _, have := range c.ids {
				if have == id {
					alive = true
					break
				}
			}
			if !alive {
				http.Error(w, `{"value":{"error":"invalid session id"}}`, http.StatusNotFound)
				return
			}
			switch {
			case r.Method == http.MethodGet && tail == "url":
				_ = json.NewEncoder(w).Encode(map[string]any{"value": c.urlBySession[id]})
			case r.Method == http.MethodPost && tail == "url":
				raw, _ := io.ReadAll(r.Body)
				var body struct {
					URL string `json:"url"`
				}
				_ = json.Unmarshal(raw, &body)
				c.navigations = append(c.navigations, body.URL)
				c.urlBySession[id] = body.URL
				_ = json.NewEncoder(w).Encode(map[string]any{"value": nil})
			case r.Method == http.MethodDelete && tail == "":
				c.deletes++
				next := c.ids[:0]
				for _, have := range c.ids {
					if have != id {
						next = append(next, have)
					}
				}
				c.ids = next
				delete(c.urlBySession, id)
				_ = json.NewEncoder(w).Encode(map[string]any{"value": nil})
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	})
}

func newLeaseServer(t *testing.T, stub *warmStub, cd *cdStub) *httptest.Server {
	t.Helper()
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)
	driver := httptest.NewServer(cd.handler())
	t.Cleanup(driver.Close)

	content := `
slots:
  - id: pool-chrome-1
    protocol: webdriver
    browser: chrome
    warm_url: ` + warm.URL + `
    webdriver_url: ` + driver.URL + `/
    webdriver_url_loopback: ` + driver.URL + `/
  - id: pool-hot-chrome-min-1
    protocol: webdriver
    browser: chrome
    pool: hot
    warm_url: ` + warm.URL + `
    webdriver_url: ` + driver.URL + `/
    webdriver_url_loopback: ` + driver.URL + `/
  - id: pool-hot-pw-min-1
    protocol: playwright
    browser: chromium
    pool: hot
    warm_url: ` + warm.URL + `
    playwright_ws_url: ws://127.0.0.1:16441/
    playwright_ws_url_loopback: ws://127.0.0.1:16441/
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := loadPool(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer((&server{pool: pool}).routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestLeaseCreatesThenReusesSession(t *testing.T) {
	stub := &warmStub{}
	cd := &cdStub{}
	ts := newLeaseServer(t, stub, cd)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{
		"owner":    "ci-1",
		"loopback": true,
		"url":      "https://example.com/login",
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("lease: %d %v", code, body)
	}
	if body["created"] != true {
		t.Fatalf("first lease should create: %v", body)
	}
	sid := body["sessionId"].(string)
	if sid == "" {
		t.Fatal("missing sessionId")
	}
	slot := body["slot"].(map[string]any)
	if slot["id"] != "pool-hot-chrome-min-1" {
		t.Fatalf("must pick hot WD, got %v", slot["id"])
	}
	if slot["driverSessionId"] != sid {
		t.Fatalf("driverSessionId=%v want %s", slot["driverSessionId"], sid)
	}
	cd.mu.Lock()
	if cd.creates != 1 || len(cd.navigations) != 1 {
		t.Fatalf("creates=%d nav=%v", cd.creates, cd.navigations)
	}
	cd.mu.Unlock()

	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{
		"slotId": "pool-hot-chrome-min-1",
	})
	if code != 200 {
		t.Fatalf("release: %d %v", code, body)
	}
	cd.mu.Lock()
	if cd.deletes != 0 {
		t.Fatalf("release must keep session, deletes=%d", cd.deletes)
	}
	cd.mu.Unlock()
	if stub.resetN.Load() != 1 {
		t.Fatalf("reset=%d", stub.resetN.Load())
	}

	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{
		"owner":    "ci-2",
		"loopback": true,
	})
	if code != 200 || body["created"] != false {
		t.Fatalf("second lease should reuse: %d %v", code, body)
	}
	if body["sessionId"] != sid {
		t.Fatalf("sessionId=%v want %s", body["sessionId"], sid)
	}
	cd.mu.Lock()
	if cd.creates != 1 {
		t.Fatalf("creates=%d want 1", cd.creates)
	}
	cd.mu.Unlock()
}

func TestLeaseRejectsWarmPoolAndWarmSlot(t *testing.T) {
	ts := newLeaseServer(t, &warmStub{}, &cdStub{})
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{"pool": "warm"})
	if code != 400 || body["error"] != leaseHotOnly {
		t.Fatalf("warm pool: %d %v", code, body)
	}
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{
		"slotId": "pool-chrome-1",
	})
	if code != 400 || body["error"] != leaseHotOnly {
		t.Fatalf("warm slotId: %d %v", code, body)
	}
}

func TestLease409WhenBusy(t *testing.T) {
	ts := newLeaseServer(t, &warmStub{}, &cdStub{})
	code, _ := doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{"owner": "a", "loopback": true})
	if code != 200 {
		t.Fatalf("first: %d", code)
	}
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{"owner": "b", "loopback": true})
	if code != 409 || body["error"] != "no available slots" {
		t.Fatalf("busy: %d %v", code, body)
	}
}

func TestLeasePlaywrightGoto(t *testing.T) {
	stub := &warmStub{}
	ts := newLeaseServer(t, stub, &cdStub{})
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{
		"protocol": "playwright",
		"browser":  "chromium",
		"owner":    "pw-1",
		"loopback": true,
		"url":      "https://example.com/login",
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("lease pw: %d %v", code, body)
	}
	if body["created"] != false {
		t.Fatalf("pw created=%v", body["created"])
	}
	if body["playwrightWsUrl"] != "ws://127.0.0.1:16441/" {
		t.Fatalf("ws=%v", body["playwrightWsUrl"])
	}
	if _, ok := body["sessionId"]; ok {
		t.Fatalf("pw must not return WD sessionId: %v", body)
	}
	if !stub.called("POST /warm/goto") {
		t.Fatal("expected /warm/goto")
	}
}

func TestReleaseKillSessionDeletesDriver(t *testing.T) {
	cd := &cdStub{}
	ts := newLeaseServer(t, &warmStub{}, cd)
	_, body := doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{"owner": "k", "loopback": true})
	sid := body["sessionId"].(string)
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{
		"slotId":      "pool-hot-chrome-min-1",
		"killSession": true,
	})
	if code != 200 {
		t.Fatalf("release: %d %v", code, body)
	}
	cd.mu.Lock()
	if cd.deletes != 1 {
		t.Fatalf("deletes=%d", cd.deletes)
	}
	cd.mu.Unlock()

	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{"owner": "k2", "loopback": true})
	if code != 200 || body["created"] != true {
		t.Fatalf("after kill must create: %d %v", code, body)
	}
	if body["sessionId"] == sid {
		t.Fatal("expected new session id")
	}
}

func TestLeaseDialsDockerURLNotHostLoopback(t *testing.T) {
	stub := &warmStub{}
	cd := &cdStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)
	driver := httptest.NewServer(cd.handler())
	t.Cleanup(driver.Close)

	content := `
slots:
  - id: pool-hot-chrome-min-1
    protocol: webdriver
    browser: chrome
    pool: hot
    warm_url: ` + warm.URL + `
    webdriver_url: ` + driver.URL + `/
    webdriver_url_loopback: http://127.0.0.1:16440/
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := loadPool(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer((&server{pool: pool}).routes())
	t.Cleanup(ts.Close)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/lease", map[string]any{
		"owner":    "box1",
		"loopback": true,
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("lease must dial docker-DNS, not host loopback: %d %v", code, body)
	}
	if body["webdriverUrl"] != "http://127.0.0.1:16440/" {
		t.Fatalf("client URL must stay loopback, got %v", body["webdriverUrl"])
	}
	if body["created"] != true {
		t.Fatalf("created=%v", body["created"])
	}
	cd.mu.Lock()
	if cd.creates != 1 {
		t.Fatalf("creates=%d", cd.creates)
	}
	cd.mu.Unlock()
}

func TestWdEnsureSessionReusesListedID(t *testing.T) {
	cd := &cdStub{ids: []string{"already"}, urlBySession: map[string]string{"already": "about:blank"}}
	drv := httptest.NewServer(cd.handler())
	t.Cleanup(drv.Close)
	id, created, err := wdEnsureSession(drv.URL, "")
	if err != nil || created || id != "already" {
		t.Fatalf("id=%s created=%v err=%v", id, created, err)
	}
	cd.mu.Lock()
	if cd.creates != 0 {
		t.Fatalf("creates=%d", cd.creates)
	}
	cd.mu.Unlock()
}
