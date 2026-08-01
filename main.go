// Command selenoid-warm-pool is a protocol-agnostic warm browser pool manager.
//
// It is a 1:1 Go port of the Python/Flask PoC (orchestrator/main.py). The slot
// pool is loaded from a YAML config; each slot exposes the same HTTP warm API
// (see warm-api/) regardless of protocol (WebDriver / Playwright). The
// orchestrator only knows warm_url + HTTP.
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

	s.pool.mu.Lock()
	candidates := s.pool.available(protocol, browser)
	if len(candidates) == 0 {
		s.pool.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "no available slots"})
		return
	}
	slot := candidates[0]
	slot.ReservedBy = &owner
	payload := slot.payload()
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
	s.pool.mu.Unlock()

	// Best-effort reset — ignore errors, like the Python impl.
	_, _ = httpJSON("POST", warmURL+"/warm/reset", nil)

	s.pool.mu.Lock()
	slot.ReservedBy = nil
	s.pool.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "slotId": slot.ID})
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
