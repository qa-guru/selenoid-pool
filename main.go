// Command selenoid-pool is a protocol-agnostic browser slot manager
// (warm container-reuse + hot session-reuse).
//
// Warm slots: hub POST /pool/reserve → New Session on an already-up container.
// Hot slots: POST /pool/lease → attach to a live ChromeDriver UUID / Playwright WS /
// CDP HTTP DevTools (bypass hub). YAML config; WD/PW slots also expose warm-api HTTP
// (see warm-api/). CDP hot slots have no warm-api — loadPool still requires warm_url.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var warmClient = &http.Client{Timeout: 30 * time.Second}

// httpJSON performs an HTTP request to a warm slot and decodes the JSON body.
// It mirrors orchestrator/main.py::http_json: on a non-2xx response it returns
// an error carrying status + body; an empty body decodes to an empty object.
func httpJSON(method, url string, payload any) (any, error) {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	resp, err := warmClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s -> HTTP %d: %s", method, url, resp.StatusCode, string(raw))
	}

	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type server struct {
	pool   *Pool
	docker *dockerClient
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// decodeBody reads a JSON object body, tolerating an empty/invalid body the
// same way Flask's get_json(silent=True) does (falling back to {}).
func decodeBody(r *http.Request) map[string]any {
	body := map[string]any{}
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		return body
	}
	_ = json.Unmarshal(raw, &body)
	return body
}

func stringField(body map[string]any, key string) (string, bool) {
	v, ok := body[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

func boolField(body map[string]any, key string) bool {
	v, ok := body[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// numField reads a numeric JSON field (float64 after decode).
func numField(body map[string]any, key string) (float64, bool) {
	v, ok := body[key]
	if !ok || v == nil {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

// leaseTTL resolves the reservation TTL: request ttlSec (seconds) clamped to
// [60s, max_lease_ttl], else the configured default.
func (p *Pool) leaseTTL(body map[string]any) time.Duration {
	if sec, ok := numField(body, "ttlSec"); ok && sec > 0 {
		d := time.Duration(sec * float64(time.Second))
		if d < time.Minute {
			return time.Minute
		}
		if d > p.MaxLeaseTTL {
			return p.MaxLeaseTTL
		}
		return d
	}
	return p.LeaseTTL
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	s.pool.mu.Lock()
	n := len(s.pool.slots)
	s.pool.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slots": n})
}

func (s *server) listSlots(w http.ResponseWriter, _ *http.Request) {
	s.pool.mu.Lock()
	payloads := make([]slotPayload, 0, len(s.pool.slots))
	for _, slot := range s.pool.slots {
		payloads = append(payloads, slot.payload())
	}
	s.pool.mu.Unlock()
	writeJSON(w, http.StatusOK, payloads)
}

func (s *server) reserve(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	protocol, _ := stringField(body, "protocol")
	browser, _ := stringField(body, "browser")
	owner, ok := stringField(body, "owner")
	if !ok {
		owner = anonymousOwner
	}
	loopback := boolField(body, "loopback")
	wantID, _ := stringField(body, "slotId")

	s.pool.mu.Lock()
	var slot *Slot
	if wantID != "" {
		slot = s.pool.byID(wantID)
		if slot == nil || slot.ReservedBy != nil || slot.Recycling {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		if protocol != "" && slot.Protocol != protocol {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		if browser != "" && slot.Browser != browser {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		if loopback && !slot.hasLoopbackEndpoint() {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
	} else {
		candidates := s.pool.available(protocol, browser, loopback)
		if len(candidates) == 0 {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		slot = candidates[0]
	}
	slot.claim(owner, s.pool.leaseTTL(body))
	payload := slot.payloadFor(loopback)
	s.pool.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slot": payload})
}

// freeSlot performs the release side effects — optional driver-session kill,
// best-effort /warm/reset — then unclaims the slot. Called after the caller
// validated the reservation and dropped the pool lock.
func (s *server) freeSlot(slot *Slot, kill bool) {
	wdURL := slot.wdBase()
	driverID := slot.DriverSessionID
	if kill && driverID != "" {
		wdDeleteSession(wdURL, driverID)
	}

	// Best-effort reset — ignore errors, like the Python impl.
	_, _ = httpJSON("POST", slot.WarmURL+"/warm/reset", nil)

	s.pool.mu.Lock()
	slot.unclaim()
	if kill {
		slot.DriverSessionID = ""
	}
	s.pool.mu.Unlock()
}

// reservationError is the 409 payload when a release/renew owner check fails.
func reservationError(w http.ResponseWriter, slot *Slot) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": fmt.Sprintf("slot %q is reserved by %q", slot.ID, *slot.ReservedBy),
	})
}

func (s *server) release(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	slotID, ok := stringField(body, "slotId")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slotId is required"})
		return
	}
	owner, _ := stringField(body, "owner")
	force := boolField(body, "force")
	kill := boolField(body, "killSession")

	s.pool.mu.Lock()
	slot := s.pool.byID(slotID)
	if slot == nil {
		s.pool.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "slot not found"})
		return
	}
	if slot.ReservedBy == nil {
		// Already free — idempotent no-op; do not touch the browser.
		s.pool.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slotId": slot.ID, "alreadyFree": true})
		return
	}
	if !slot.canRelease(owner, force) {
		reservationError(w, slot)
		s.pool.mu.Unlock()
		return
	}
	s.pool.mu.Unlock()

	s.freeSlot(slot, kill)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slotId": slot.ID})
}

// renew extends a live reservation's lease deadline (same owner rules as release).
func (s *server) renew(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	slotID, ok := stringField(body, "slotId")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slotId is required"})
		return
	}
	owner, _ := stringField(body, "owner")
	force := boolField(body, "force")

	s.pool.mu.Lock()
	slot := s.pool.byID(slotID)
	if slot == nil {
		s.pool.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "slot not found"})
		return
	}
	if slot.ReservedBy == nil {
		s.pool.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "slot is not reserved"})
		return
	}
	if !slot.canRelease(owner, force) {
		reservationError(w, slot)
		s.pool.mu.Unlock()
		return
	}
	slot.LeaseExpiresAt = time.Now().Add(s.pool.leaseTTL(body))
	deadline := slot.LeaseExpiresAt
	s.pool.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"slotId":         slot.ID,
		"leaseExpiresAt": deadline.UTC(),
	})
}

const leaseHotOnly = "lease is for hot slots; use POST /pool/reserve for warm"

func (s *server) unreserve(slot *Slot) {
	s.pool.mu.Lock()
	slot.unclaim()
	s.pool.mu.Unlock()
}

func (s *server) lease(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	poolName, _ := stringField(body, "pool")
	if poolName == "" {
		poolName = "hot"
	}
	if !strings.EqualFold(poolName, "hot") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": leaseHotOnly})
		return
	}
	protocol, _ := stringField(body, "protocol")
	browser, _ := stringField(body, "browser")
	owner, ok := stringField(body, "owner")
	if !ok {
		owner = anonymousOwner
	}
	loopback := boolField(body, "loopback")
	wantID, _ := stringField(body, "slotId")
	pageURL, _ := stringField(body, "url")
	if wantID == "" {
		if protocol == "" {
			protocol = "webdriver"
		}
		if browser == "" && protocol == "webdriver" {
			browser = "chrome"
		}
		if browser == "" && protocol == "playwright" {
			browser = "chromium"
		}
		if browser == "" && protocol == "cdp" {
			browser = "chromium"
		}
	}

	s.pool.mu.Lock()
	var slot *Slot
	if wantID != "" {
		slot = s.pool.byID(wantID)
		if slot == nil || slot.ReservedBy != nil || slot.Recycling {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		if !slot.isHot() {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": leaseHotOnly})
			return
		}
		if protocol != "" && slot.Protocol != protocol {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		if browser != "" && slot.Browser != browser {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		if loopback && !slot.hasLoopbackEndpoint() {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
	} else {
		candidates := s.pool.availableClass("hot", protocol, browser, loopback)
		if len(candidates) == 0 {
			s.pool.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
			return
		}
		slot = candidates[0]
	}
	slot.claim(owner, s.pool.leaseTTL(body))
	knownID := slot.DriverSessionID
	payload := slot.payloadFor(loopback)
	s.pool.mu.Unlock()

	created := false
	sessionID := ""
	switch slot.Protocol {
	case "playwright":
		if pageURL != "" {
			if _, err := httpJSON("POST", slot.WarmURL+"/warm/goto", map[string]any{"url": pageURL}); err != nil {
				s.unreserve(slot)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}
	case "cdp":
		// HTTP DevTools only. No ChromeDriver session, no /warm/goto (slot has no warm-api).
		// Optional url in the body is ignored — must not 500.
	default:
		base := slot.wdDialURL()
		id, didCreate, err := wdEnsureSession(base, knownID)
		if err != nil {
			s.unreserve(slot)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		sessionID = id
		created = didCreate
		if pageURL != "" {
			if err := wdNavigate(base, sessionID, pageURL); err != nil {
				s.unreserve(slot)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}
		s.pool.mu.Lock()
		slot.DriverSessionID = sessionID
		payload = slot.payloadFor(loopback)
		s.pool.mu.Unlock()
	}

	out := map[string]any{
		"ok":      true,
		"created": created,
		"slot":    payload,
	}
	if sessionID != "" {
		out["sessionId"] = sessionID
	}
	if payload.WebdriverURL != nil {
		out["webdriverUrl"] = *payload.WebdriverURL
	}
	if payload.PlaywrightWsURL != nil {
		out["playwrightWsUrl"] = *payload.PlaywrightWsURL
	}
	if payload.CdpURL != nil {
		out["cdpUrl"] = *payload.CdpURL
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) preopen(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	slotID, hasSlot := stringField(body, "slotId")
	url, hasURL := stringField(body, "url")
	if !hasSlot || !hasURL {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slotId and url are required"})
		return
	}

	slot := s.lookup(slotID)
	if slot == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "slot not found"})
		return
	}

	result, err := httpJSON("POST", slot.WarmURL+"/warm/goto", map[string]any{"url": url})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slotId": slot.ID, "result": result})
}

func (s *server) videoStart(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	slotID, ok := stringField(body, "slotId")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slotId is required"})
		return
	}

	slot := s.lookup(slotID)
	if slot == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "slot not found"})
		return
	}

	sessionID, ok := stringField(body, "sessionId")
	if !ok {
		sessionID = slot.SessionID
	}

	result, err := httpJSON("POST", slot.WarmURL+"/warm/video/start", map[string]any{"sessionId": sessionID})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slotId": slot.ID, "result": result})
}

func (s *server) videoStop(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	slotID, ok := stringField(body, "slotId")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slotId is required"})
		return
	}

	slot := s.lookup(slotID)
	if slot == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "slot not found"})
		return
	}

	result, err := httpJSON("POST", slot.WarmURL+"/warm/video/stop", nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slotId": slot.ID, "result": result})
}

// lookup resolves a slot by id under the pool mutex.
func (s *server) lookup(id string) *Slot {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	return s.pool.byID(id)
}

// sweepExpired frees reservations whose lease deadline passed (reaper), then
// kicks container rotation for free warm slots that crossed the recycle
// thresholds. Safe to call periodically and from tests.
func (s *server) sweepExpired(now time.Time) {
	s.pool.mu.Lock()
	type victim struct {
		slot  *Slot
		owner string
	}
	var expired []victim
	for _, slot := range s.pool.slots {
		if slot.expired(now) {
			expired = append(expired, victim{slot: slot, owner: *slot.ReservedBy})
		}
	}
	s.pool.mu.Unlock()

	for _, v := range expired {
		log.Printf("[REAPER] lease expired: %s (owner %s)", v.slot.ID, v.owner)
		s.freeSlot(v.slot, false)
	}
	s.sweepRecycle(now)
}

// sweepRecycle marks due warm slots Recycling and restarts them in the
// background. Slots without a restart path (no docker socket, no container
// name) are flagged RecyclePending and keep serving — availability is never
// silently reduced.
func (s *server) sweepRecycle(now time.Time) {
	p := s.pool
	if p.RecycleMaxSessions <= 0 && p.RecycleMaxAge <= 0 {
		return
	}
	p.mu.Lock()
	var due []*Slot
	for _, slot := range p.slots {
		if !slot.dueForRecycle(p.RecycleMaxSessions, p.RecycleMaxAge, now) {
			continue
		}
		if now.Sub(slot.LastRecycleAttempt) < time.Minute {
			continue
		}
		slot.Recycling = true
		slot.LastRecycleAttempt = now
		due = append(due, slot)
	}
	p.mu.Unlock()
	for _, slot := range due {
		go s.recycleSlot(slot)
	}
}

// recycleSlot restarts the slot container via docker and re-arms its counters
// once the warm endpoint answers TCP again.
func (s *server) recycleSlot(slot *Slot) {
	fail := func(err error) {
		log.Printf("[RECYCLE] %s failed: %v", slot.ID, err)
		s.pool.mu.Lock()
		slot.Recycling = false
		slot.RecyclePending = true
		s.pool.mu.Unlock()
	}
	if s.docker == nil {
		fail(fmt.Errorf("docker socket unavailable"))
		return
	}
	name := slot.containerName()
	if name == "" {
		fail(fmt.Errorf("no container name (set container: in the slot config)"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := s.docker.RestartContainer(ctx, name); err != nil {
		fail(err)
		return
	}
	if err := waitTCPOpen(slot.WarmURL, 90*time.Second); err != nil {
		fail(err)
		return
	}
	s.pool.mu.Lock()
	slot.StartedAt = time.Now()
	slot.SessionCount = 0
	slot.Recycling = false
	slot.RecyclePending = false
	s.pool.mu.Unlock()
	log.Printf("[RECYCLE] %s restarted container %s", slot.ID, name)
}

// waitTCPOpen polls the warm_url host:port until a TCP connect succeeds —
// protocol-agnostic readiness after a container restart.
func waitTCPOpen(rawURL string, timeout time.Duration) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("bad warm url %q", rawURL)
	}
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s not reachable after restart", u.Host)
		}
		time.Sleep(time.Second)
	}
}

// reaperLoop runs the lease reaper + recycler sweep until ctx is cancelled.
func (s *server) reaperLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.sweepExpired(now)
		}
	}
}

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	// GET / must be 2xx — stand URL gate probes the root path.
	mux.HandleFunc("GET /{$}", s.health)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /pool/slots", s.listSlots)
	mux.HandleFunc("POST /pool/reserve", s.reserve)
	mux.HandleFunc("POST /pool/release", s.release)
	mux.HandleFunc("POST /pool/renew", s.renew)
	mux.HandleFunc("POST /pool/lease", s.lease)
	mux.HandleFunc("POST /pool/preopen", s.preopen)
	mux.HandleFunc("POST /pool/video/start", s.videoStart)
	mux.HandleFunc("POST /pool/video/stop", s.videoStop)
	return mux
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	defConfig := envOr("WARM_POOL_CONFIG", "config.example.yaml")
	defHost := envOr("WARM_POOL_HOST", "0.0.0.0")
	defPort := 9090
	if v := os.Getenv("WARM_POOL_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			defPort = p
		}
	}

	config := flag.String("config", defConfig, "Path to pool config YAML")
	host := flag.String("host", defHost, "Bind host")
	port := flag.Int("port", defPort, "Bind port")
	flag.Parse()

	if _, err := os.Stat(*config); err != nil {
		fmt.Fprintf(os.Stderr, "Config not found: %s\n", *config)
		os.Exit(1)
	}

	pool, err := loadPool(*config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	srv := &server{pool: pool}
	if sock := envOr("POOL_DOCKER_SOCKET", "/var/run/docker.sock"); sock != "" {
		if _, err := os.Stat(sock); err == nil {
			srv.docker = newDockerClient(sock)
			log.Printf("[INIT] docker socket %s — warm recycle enabled", sock)
		} else {
			log.Printf("[INIT] no docker socket at %s — warm recycle will report recyclePending", sock)
		}
	}
	log.Printf("[INIT] lease_ttl=%s max_lease_ttl=%s recycle sessions>=%d age>=%s",
		pool.LeaseTTL, pool.MaxLeaseTTL, pool.RecycleMaxSessions, pool.RecycleMaxAge)
	go srv.reaperLoop(context.Background(), 15*time.Second)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("selenoid-pool listening on %s (%d slots)", addr, len(pool.slots))

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
