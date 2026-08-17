// Command selenoid-warm-pool is a protocol-agnostic browser slot manager
// (warm container-reuse + hot session-reuse). Repo may later rename to selenoid-pool.
//
// Warm slots: hub POST /pool/reserve → New Session on an already-up container.
// Hot slots: POST /pool/lease → attach to a live ChromeDriver UUID / Playwright WS
// (bypass hub). YAML config; each slot also exposes warm-api HTTP (see warm-api/).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
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
	pool *Pool
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
		owner = "anonymous"
	}
	loopback := boolField(body, "loopback")
	wantID, _ := stringField(body, "slotId")

	s.pool.mu.Lock()
	var slot *Slot
	if wantID != "" {
		slot = s.pool.byID(wantID)
		if slot == nil || slot.ReservedBy != nil {
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
	slot.ReservedBy = &owner
	payload := slot.payloadFor(loopback)
	s.pool.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slot": payload})
}

func (s *server) release(w http.ResponseWriter, r *http.Request) {
	body := decodeBody(r)
	slotID, ok := stringField(body, "slotId")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slotId is required"})
		return
	}

	s.pool.mu.Lock()
	slot := s.pool.byID(slotID)
	if slot == nil {
		s.pool.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "slot not found"})
		return
	}
	warmURL := slot.WarmURL
	kill := boolField(body, "killSession")
	wdURL := slot.wdBase()
	driverID := slot.DriverSessionID
	s.pool.mu.Unlock()

	if kill && driverID != "" {
		wdDeleteSession(wdURL, driverID)
	}

	// Best-effort reset — ignore errors, like the Python impl.
	_, _ = httpJSON("POST", warmURL+"/warm/reset", nil)

	s.pool.mu.Lock()
	slot.ReservedBy = nil
	if kill {
		slot.DriverSessionID = ""
	}
	s.pool.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slotId": slot.ID})
}

const leaseHotOnly = "lease is for hot slots; use POST /pool/reserve for warm"

func (s *server) unreserve(slot *Slot) {
	s.pool.mu.Lock()
	slot.ReservedBy = nil
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
		owner = "anonymous"
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
	}

	s.pool.mu.Lock()
	var slot *Slot
	if wantID != "" {
		slot = s.pool.byID(wantID)
		if slot == nil || slot.ReservedBy != nil {
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
	slot.ReservedBy = &owner
	knownID := slot.DriverSessionID
	payload := slot.payloadFor(loopback)
	s.pool.mu.Unlock()

	created := false
	sessionID := ""
	if slot.Protocol == "playwright" {
		if pageURL != "" {
			if _, err := httpJSON("POST", slot.WarmURL+"/warm/goto", map[string]any{"url": pageURL}); err != nil {
				s.unreserve(slot)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}
	} else {
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

func (s *server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	// GET / must be 2xx — stand URL gate probes the root path.
	mux.HandleFunc("GET /{$}", s.health)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /pool/slots", s.listSlots)
	mux.HandleFunc("POST /pool/reserve", s.reserve)
	mux.HandleFunc("POST /pool/release", s.release)
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
	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("selenoid-warm-pool listening on %s (%d slots)", addr, len(pool.slots))

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
