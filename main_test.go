package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func writeTempConfig(t *testing.T, warmURL string) string {
	t.Helper()
	content := `
slots:
  - id: slot-webdriver-1
    protocol: webdriver
    browser: chrome
    session_id: slot-webdriver-1
    warm_url: ` + warmURL + `
    webdriver_url: http://warm-chrome-1:4444/wd/hub
  - id: slot-pw-1
    protocol: playwright
    browser: chromium
    session_id: slot-pw-1
    warm_url: ` + warmURL + `
    playwright_ws_url: ws://warm-pw-1:3000/
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type warmStub struct {
	mu     sync.Mutex
	calls  []string
	resetN atomic.Int32
	fail   map[string]int // path → status (0 = 200 + {"ok":true})
}

func (w *warmStub) handler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.mu.Lock()
		w.calls = append(w.calls, r.Method+" "+r.URL.Path)
		status := w.fail[r.URL.Path]
		w.mu.Unlock()

		if r.URL.Path == "/warm/reset" {
			w.resetN.Add(1)
		}

		body, _ := io.ReadAll(r.Body)
		_ = body

		if status != 0 {
			http.Error(rw, `{"error":"stub"}`, status)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"ok":true}`))
	})
}

func (w *warmStub) called(substr string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func newTestServer(t *testing.T, stub *warmStub) *httptest.Server {
	t.Helper()
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	pool, err := loadPool(writeTempConfig(t, warm.URL))
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{pool: pool}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			// list endpoints return arrays
			var arr any
			if err2 := json.Unmarshal(raw, &arr); err2 == nil {
				return resp.StatusCode, map[string]any{"_list": arr}
			}
			t.Fatalf("decode %s: %v body=%s", url, err, raw)
		}
	}
	return resp.StatusCode, out
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	code, body := doJSON(t, http.MethodGet, ts.URL+"/health", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if body["ok"] != true {
		t.Fatalf("ok=%v", body["ok"])
	}
	if body["slots"] != float64(2) {
		t.Fatalf("slots=%v", body["slots"])
	}
}

func TestReserveReleaseAnd409(t *testing.T) {
	stub := &warmStub{}
	ts := newTestServer(t, stub)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    "jenkins-1",
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("reserve: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	if slot["id"] != "slot-webdriver-1" {
		t.Fatalf("slot id=%v", slot["id"])
	}
	if slot["reservedBy"] != "jenkins-1" {
		t.Fatalf("reservedBy=%v", slot["reservedBy"])
	}

	// second webdriver chrome → 409
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    "jenkins-2",
	})
	if code != 409 || body["error"] != "no available slots" {
		t.Fatalf("want 409, got %d %v", code, body)
	}

	slotID := slot["id"].(string)
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{"slotId": slotID})
	if code != 200 || body["ok"] != true {
		t.Fatalf("release: %d %v", code, body)
	}
	if stub.resetN.Load() != 1 {
		t.Fatalf("reset calls=%d", stub.resetN.Load())
	}

	// freed → reserve again
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"owner":    "jenkins-3",
	})
	if code != 200 {
		t.Fatalf("re-reserve: %d %v", code, body)
	}
}

func TestReleaseBestEffortIgnoresResetErrors(t *testing.T) {
	stub := &warmStub{fail: map[string]int{"/warm/reset": 500}}
	ts := newTestServer(t, stub)

	_, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{"owner": "x"})
	slotID := body["slot"].(map[string]any)["id"].(string)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{"slotId": slotID})
	if code != 200 || body["ok"] != true {
		t.Fatalf("release should swallow reset errors: %d %v", code, body)
	}
}

func TestReleaseRequiresSlotID(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{})
	if code != 400 || body["error"] != "slotId is required" {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestReleaseUnknownSlot404(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{"slotId": "missing"})
	if code != 404 || body["error"] != "slot not found" {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestPreopenNavigatesReservedSlot(t *testing.T) {
	stub := &warmStub{}
	ts := newTestServer(t, stub)

	_, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{"owner": "jenkins-4"})
	slotID := body["slot"].(map[string]any)["id"].(string)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/preopen", map[string]any{
		"slotId": slotID,
		"url":    "https://example.com/login",
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("preopen: %d %v", code, body)
	}
	if body["slotId"] != slotID {
		t.Fatalf("slotId=%v", body["slotId"])
	}
	result, _ := body["result"].(map[string]any)
	if result["ok"] != true {
		t.Fatalf("result=%v", body["result"])
	}
	if !stub.called("POST /warm/goto") {
		t.Fatal("expected /warm/goto")
	}
}

func TestPreopenRequiresSlotIDAndURL(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/preopen", map[string]any{"slotId": "slot-webdriver-1"})
	if code != 400 || body["error"] != "slotId and url are required" {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestPreopenUnknownSlot404(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	code, _ := doJSON(t, http.MethodPost, ts.URL+"/pool/preopen", map[string]any{
		"slotId": "missing",
		"url":    "https://example.com",
	})
	if code != 404 {
		t.Fatalf("status=%d", code)
	}
}

func TestPreopenSurfacesWarmErrors(t *testing.T) {
	stub := &warmStub{fail: map[string]int{"/warm/goto": 502}}
	ts := newTestServer(t, stub)
	_, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{"owner": "e"})
	slotID := body["slot"].(map[string]any)["id"].(string)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/preopen", map[string]any{
		"slotId": slotID,
		"url":    "https://example.com",
	})
	if code != 500 {
		t.Fatalf("want 500, got %d %v", code, body)
	}
	errStr, _ := body["error"].(string)
	if !strings.Contains(errStr, "HTTP 502") {
		t.Fatalf("error=%q", errStr)
	}
}

func TestVideoStartAndStop(t *testing.T) {
	stub := &warmStub{}
	ts := newTestServer(t, stub)

	_, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{"owner": "v"})
	slotID := body["slot"].(map[string]any)["id"].(string)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/video/start", map[string]any{
		"slotId":    slotID,
		"sessionId": "custom-session",
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("video/start: %d %v", code, body)
	}

	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/video/stop", map[string]any{"slotId": slotID})
	if code != 200 || body["ok"] != true {
		t.Fatalf("video/stop: %d %v", code, body)
	}
	if !stub.called("POST /warm/video/start") || !stub.called("POST /warm/video/stop") {
		t.Fatalf("calls=%v", stub.calls)
	}
}

func TestConcurrentReserveUsesMutex(t *testing.T) {
	// One slot only — concurrent reserve must yield exactly one 200 and rest 409.
	stub := &warmStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	content := `
slots:
  - id: only
    warm_url: ` + warm.URL + `
`
	path := filepath.Join(t.TempDir(), "one.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := loadPool(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer((&server{pool: pool}).routes())
	t.Cleanup(ts.Close)

	const n = 32
	var okCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			raw := []byte(`{"owner":"worker-` + string(rune('a'+i%26)) + `"}`)
			resp, err := http.Post(ts.URL+"/pool/reserve", "application/json", strings.NewReader(string(raw)))
			if err != nil {
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.StatusCode == 200 {
				okCount.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("expected exactly 1 successful reserve, got %d", okCount.Load())
	}
}

func TestReserveLoopback409WhenOnlyDockerDNS(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    "hub-1",
		"loopback": true,
	})
	if code != 409 || body["error"] != "no available slots" {
		t.Fatalf("want 409 for docker-DNS slots, got %d %v", code, body)
	}
}

func TestReserveLoopbackPrefersLoopbackURL(t *testing.T) {
	stub := &warmStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	loop := "http://127.0.0.1:14441/"
	content := `
slots:
  - id: slot-webdriver-1
    protocol: webdriver
    browser: chrome
    warm_url: ` + warm.URL + `
    webdriver_url: http://warm-chrome-1:4444/
    webdriver_url_loopback: ` + loop + `
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

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    "hub-1",
		"loopback": true,
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("reserve: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	if slot["webdriverUrl"] != loop {
		t.Fatalf("webdriverUrl=%v want %s", slot["webdriverUrl"], loop)
	}
	if slot["webdriverUrlLoopback"] != loop {
		t.Fatalf("webdriverUrlLoopback=%v", slot["webdriverUrlLoopback"])
	}
}

func TestReserveWithoutLoopbackKeepsDockerDNS(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    "jenkins-1",
	})
	if code != 200 {
		t.Fatalf("reserve: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	if slot["webdriverUrl"] != "http://warm-chrome-1:4444/wd/hub" {
		t.Fatalf("webdriverUrl=%v", slot["webdriverUrl"])
	}
}

func TestIsLoopbackURL(t *testing.T) {
	if !isLoopbackURL("http://127.0.0.1:14441/") || isLoopbackURL("http://warm-chrome-1:4444/") {
		t.Fatal("isLoopbackURL mismatch")
	}
	if !isLoopbackURL("ws://127.0.0.1:14501/") || isLoopbackURL("ws://warm-pw-1:3000/") {
		t.Fatal("isLoopbackURL ws mismatch")
	}
}

func TestReservePlaywrightLoopbackPrefersLoopbackWS(t *testing.T) {
	stub := &warmStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	loop := "ws://127.0.0.1:14501/"
	content := `
slots:
  - id: slot-pw-1
    protocol: playwright
    browser: chromium
    warm_url: ` + warm.URL + `
    playwright_ws_url: ws://warm-pw-1:3000/
    playwright_ws_url_loopback: ` + loop + `
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

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "playwright",
		"browser":  "chromium",
		"owner":    "hot-1",
		"loopback": true,
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("reserve: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	if slot["playwrightWsUrl"] != loop {
		t.Fatalf("playwrightWsUrl=%v want %s", slot["playwrightWsUrl"], loop)
	}
}

func TestReserveLoopbackChromeSkipsPlaywrightSlots(t *testing.T) {
	stub := &warmStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	content := `
slots:
  - id: slot-pw-1
    protocol: playwright
    browser: chromium
    warm_url: ` + warm.URL + `
    playwright_ws_url_loopback: ws://127.0.0.1:14501/
  - id: slot-webdriver-1
    protocol: webdriver
    browser: chrome
    warm_url: ` + warm.URL + `
    webdriver_url: http://warm-chrome-1:4444/
    webdriver_url_loopback: http://127.0.0.1:14441/
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

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    "hub-1",
		"loopback": true,
	})
	if code != 200 {
		t.Fatalf("reserve: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	if slot["id"] != "slot-webdriver-1" {
		t.Fatalf("hub chrome must skip PW slots, got %v", slot["id"])
	}
}

func TestReserveSlotIdPinsHotSlot(t *testing.T) {
	stub := &warmStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	content := `
slots:
  - id: pool-chrome-1
    protocol: webdriver
    browser: chrome
    warm_url: ` + warm.URL + `
    webdriver_url_loopback: http://127.0.0.1:14441/
  - id: pool-hot-chrome-min-1
    protocol: webdriver
    browser: chrome
    warm_url: ` + warm.URL + `
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

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    "hot-1",
		"loopback": true,
		"slotId":   "pool-hot-chrome-min-1",
	})
	if code != 200 {
		t.Fatalf("reserve: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	if slot["id"] != "pool-hot-chrome-min-1" {
		t.Fatalf("slotId pin failed, got %v", slot["id"])
	}
	if slot["webdriverUrl"] != "http://127.0.0.1:16440/" {
		t.Fatalf("webdriverUrl=%v", slot["webdriverUrl"])
	}
}

func TestLoadPoolDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte(`
slots:
  - id: bare
    warm_url: http://example:8080/
`), 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := loadPool(path)
	if err != nil {
		t.Fatal(err)
	}
	s := pool.slots[0]
	if s.Protocol != "webdriver" || s.Browser != "chrome" || s.SessionID != "bare" {
		t.Fatalf("defaults: %+v", s)
	}
	if s.WarmURL != "http://example:8080" {
		t.Fatalf("trim slash: %q", s.WarmURL)
	}
}
