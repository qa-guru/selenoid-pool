package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func reserveOwner(t *testing.T, url, owner string) map[string]any {
	t.Helper()
	code, body := doJSON(t, http.MethodPost, url+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
		"owner":    owner,
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("reserve %s: %d %v", owner, code, body)
	}
	return body["slot"].(map[string]any)
}

func TestReleaseRequiresMatchingOwner(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	slot := reserveOwner(t, ts.URL, "jenkins-1")
	slotID := slot["id"].(string)

	// No owner → 409, slot stays reserved, browser untouched.
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{"slotId": slotID})
	if code != 409 {
		t.Fatalf("release without owner: %d %v", code, body)
	}
	if !strings.Contains(body["error"].(string), `"jenkins-1"`) {
		t.Fatalf("error must name the owner: %v", body)
	}

	// Wrong owner → 409.
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{
		"slotId": slotID,
		"owner":  "jenkins-2",
	})
	if code != 409 {
		t.Fatalf("release with wrong owner: %d %v", code, body)
	}

	// force → allowed for ops cleanup.
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{
		"slotId": slotID,
		"owner":  "ops",
		"force":  true,
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("force release: %d %v", code, body)
	}
}

func TestReleaseAnonymousSlotNeedsNoOwner(t *testing.T) {
	ts := newTestServer(t, &warmStub{})

	// Reserve with no owner → reservedBy "anonymous" → anyone may release.
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"protocol": "webdriver",
		"browser":  "chrome",
	})
	if code != 200 {
		t.Fatalf("reserve: %d %v", code, body)
	}
	slot := body["slot"].(map[string]any)
	if slot["reservedBy"] != anonymousOwner {
		t.Fatalf("reservedBy=%v", slot["reservedBy"])
	}
	slotID := slot["id"].(string)
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{"slotId": slotID})
	if code != 200 || body["ok"] != true {
		t.Fatalf("anonymous release: %d %v", code, body)
	}
}

func TestReleaseFreeSlotIsIdempotentNoop(t *testing.T) {
	stub := &warmStub{}
	ts := newTestServer(t, stub)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/release", map[string]any{"slotId": "slot-webdriver-1"})
	if code != 200 || body["ok"] != true || body["alreadyFree"] != true {
		t.Fatalf("alreadyFree: %d %v", code, body)
	}
	if stub.resetN.Load() != 0 {
		t.Fatalf("free slot must not be reset, resets=%d", stub.resetN.Load())
	}
}

func TestReserveLeaseDeadlinesAndSessionCount(t *testing.T) {
	ts := newTestServer(t, &warmStub{})
	slot := reserveOwner(t, ts.URL, "jenkins-1")

	if slot["reservedAt"] == nil || slot["leaseExpiresAt"] == nil {
		t.Fatalf("lease timestamps missing: %v", slot)
	}
	exp, err := time.Parse(time.RFC3339Nano, slot["leaseExpiresAt"].(string))
	if err != nil {
		t.Fatalf("leaseExpiresAt parse: %v", err)
	}
	if d := time.Until(exp); d < 50*time.Minute || d > 70*time.Minute {
		t.Fatalf("default lease ~1h, got %s", d)
	}
	if slot["sessionCount"] != float64(1) {
		t.Fatalf("sessionCount=%v", slot["sessionCount"])
	}
	if _, ok := slot["ageSec"].(float64); !ok {
		t.Fatalf("ageSec=%v", slot)
	}
}

func TestReserveTTLSecClamp(t *testing.T) {
	stub := &warmStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	content := `
lease_ttl: "10m"
max_lease_ttl: "30m"
slots:
  - id: only
    warm_url: ` + warm.URL + `
`
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := loadPool(path)
	if err != nil {
		t.Fatal(err)
	}
	if pool.LeaseTTL != 10*time.Minute || pool.MaxLeaseTTL != 30*time.Minute {
		t.Fatalf("ttls=%s/%s", pool.LeaseTTL, pool.MaxLeaseTTL)
	}
	ts := httptest.NewServer((&server{pool: pool}).routes())
	t.Cleanup(ts.Close)

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{
		"owner":  "x",
		"ttlSec": 999999,
	})
	if code != 200 {
		t.Fatalf("reserve: %d", code)
	}
	slot := body["slot"].(map[string]any)
	exp, _ := time.Parse(time.RFC3339Nano, slot["leaseExpiresAt"].(string))
	if d := time.Until(exp); d > 31*time.Minute {
		t.Fatalf("ttlSec must clamp to max_lease_ttl, got %s", d)
	}
}

func oneSlotServer(t *testing.T, stub *warmStub) (*httptest.Server, *Pool) {
	t.Helper()
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)
	content := `
slots:
  - id: only
    protocol: webdriver
    browser: chrome
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
	return ts, pool
}

func TestReaperFreesExpiredLease(t *testing.T) {
	stub := &warmStub{}
	ts, pool := oneSlotServer(t, stub)
	slot := reserveOwner(t, ts.URL, "dead-agent")
	slotID := slot["id"].(string)

	// Simulate an agent that died before release: push the deadline into the past.
	pool.mu.Lock()
	pool.byID(slotID).LeaseExpiresAt = time.Now().Add(-time.Minute)
	pool.mu.Unlock()

	(&server{pool: pool}).sweepExpired(time.Now())

	pool.mu.Lock()
	if pool.byID(slotID).ReservedBy != nil {
		pool.mu.Unlock()
		t.Fatal("expired slot still reserved")
	}
	pool.mu.Unlock()
	if stub.resetN.Load() != 1 {
		t.Fatalf("reaper must reset the slot, resets=%d", stub.resetN.Load())
	}

	// Slot is free again.
	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/reserve", map[string]any{"owner": "next"})
	if code != 200 || body["ok"] != true {
		t.Fatalf("re-reserve after reaper: %d %v", code, body)
	}
}

func TestRenewExtendsDeadlineAndChecksOwner(t *testing.T) {
	ts, pool := oneSlotServer(t, &warmStub{})
	slot := reserveOwner(t, ts.URL, "jenkins-1")
	slotID := slot["id"].(string)
	first, _ := time.Parse(time.RFC3339Nano, slot["leaseExpiresAt"].(string))

	code, body := doJSON(t, http.MethodPost, ts.URL+"/pool/renew", map[string]any{
		"slotId": slotID,
		"owner":  "other",
	})
	if code != 409 {
		t.Fatalf("renew by non-owner: %d %v", code, body)
	}
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/renew", map[string]any{
		"slotId": slotID,
		"owner":  "jenkins-1",
		"ttlSec": 120,
	})
	if code != 200 || body["ok"] != true {
		t.Fatalf("renew: %d %v", code, body)
	}
	next, _ := time.Parse(time.RFC3339Nano, body["leaseExpiresAt"].(string))
	if d := time.Until(next); d < 100*time.Second || d > 130*time.Second {
		t.Fatalf("renew must reset deadline to now+ttlSec, got %s", d)
	}
	// Renew without ttlSec restores the default lease (1h > the 2m above).
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/renew", map[string]any{
		"slotId": slotID,
		"owner":  "jenkins-1",
	})
	if code != 200 {
		t.Fatalf("renew default: %d %v", code, body)
	}
	extended, _ := time.Parse(time.RFC3339Nano, body["leaseExpiresAt"].(string))
	if !extended.After(first) {
		t.Fatalf("default renew must extend past %s, got %s", first, extended)
	}

	// Renew on a free slot → 409.
	pool.mu.Lock()
	pool.byID(slotID).unclaim()
	pool.mu.Unlock()
	code, body = doJSON(t, http.MethodPost, ts.URL+"/pool/renew", map[string]any{
		"slotId": slotID,
		"owner":  "jenkins-1",
	})
	if code != 409 {
		t.Fatalf("renew free slot: %d %v", code, body)
	}
}

func TestDockerRestartOverUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var restarted atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/restart") {
			restarted.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	dc := newDockerClient(sock)
	if err := dc.RestartContainer(context.Background(), "warm-chrome-1"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if restarted.Load() != 1 {
		t.Fatalf("restarts=%d", restarted.Load())
	}
	if err := dc.RestartContainer(context.Background(), "missing"); err == nil {
		t.Fatal("missing container must error")
	}
}

func TestRecycleRotatesWarmSlot(t *testing.T) {
	stub := &warmStub{}
	warm := httptest.NewServer(stub.handler())
	t.Cleanup(warm.Close)

	content := `
recycle_after_sessions: 2
slots:
  - id: pool-chrome-1
    protocol: webdriver
    browser: chrome
    warm_url: ` + warm.URL + `
`
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	pool, err := loadPool(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{pool: pool, docker: &dockerClient{}}
	slot := pool.slots[0]
	slot.SessionCount = 2

	// docker client with no socket → fail path: pending flag, still free to serve.
	srv.recycleSlot(slot)
	if slot.Recycling || !slot.RecyclePending {
		t.Fatalf("flags recycling=%v pending=%v", slot.Recycling, slot.RecyclePending)
	}
	if !slot.dueForRecycle(2, 0, time.Now()) {
		t.Fatal("pending slot stays due")
	}
}

func TestContainerNameFromWarmURL(t *testing.T) {
	s := &Slot{WarmURL: "http://warm-chrome-1:4444"}
	if got := s.containerName(); got != "warm-chrome-1" {
		t.Fatalf("name=%q", got)
	}
	s = &Slot{WarmURL: "http://127.0.0.1:14441"}
	if got := s.containerName(); got != "" {
		t.Fatalf("loopback must not derive a name, got %q", got)
	}
	s = &Slot{WarmURL: "http://127.0.0.1:14441", Container: "warm-chrome-9"}
	if got := s.containerName(); got != "warm-chrome-9" {
		t.Fatalf("explicit container=%q", got)
	}
}
