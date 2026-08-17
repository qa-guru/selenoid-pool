package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var wdHTTP = &http.Client{Timeout: 45 * time.Second}

func wdBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func wdDo(method, url string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := wdHTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func parseDriverSessionID(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var env struct {
		SessionID string          `json:"sessionId"`
		Value     json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	if id := sessionIDFromValue(env.Value); id != "" {
		return id
	}
	return strings.TrimSpace(env.SessionID)
}

func sessionIDFromValue(value json.RawMessage) string {
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var obj struct {
		SessionID string `json:"sessionId"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(value, &obj); err == nil {
		if obj.SessionID != "" {
			return obj.SessionID
		}
		if obj.ID != "" {
			return obj.ID
		}
	}
	var s string
	if err := json.Unmarshal(value, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func parseSessionIDs(raw []byte) []string {
	var env struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	if id := sessionIDFromValue(env.Value); id != "" {
		return []string{id}
	}
	var items []json.RawMessage
	if err := json.Unmarshal(env.Value, &items); err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if id := sessionIDFromValue(item); id != "" {
			out = append(out, id)
			continue
		}
		var s string
		if err := json.Unmarshal(item, &s); err == nil && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func wdListSessions(base string) ([]string, error) {
	st, raw, err := wdDo(http.MethodGet, wdBaseURL(base)+"/sessions", nil)
	if err != nil {
		return nil, err
	}
	if st == http.StatusNotFound {
		return nil, nil
	}
	if st < 200 || st >= 300 {
		return nil, fmt.Errorf("GET /sessions -> HTTP %d: %s", st, raw)
	}
	return parseSessionIDs(raw), nil
}

func wdCreateSession(base string) (string, error) {
	payload := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{
				"browserName": "chrome",
				"goog:chromeOptions": map[string]any{
					"args": []string{"--headless=new", "--disable-dev-shm-usage"},
				},
			},
		},
	}
	st, raw, err := wdDo(http.MethodPost, wdBaseURL(base)+"/session", payload)
	if err != nil {
		return "", err
	}
	if st < 200 || st >= 300 {
		return "", fmt.Errorf("POST /session -> HTTP %d: %s", st, raw)
	}
	id := parseDriverSessionID(raw)
	if id == "" {
		return "", fmt.Errorf("POST /session: no session id in %s", raw)
	}
	return id, nil
}

func wdNavigate(base, sessionID, pageURL string) error {
	st, raw, err := wdDo(http.MethodPost, wdBaseURL(base)+"/session/"+sessionID+"/url", map[string]any{"url": pageURL})
	if err != nil {
		return err
	}
	if st < 200 || st >= 300 {
		return fmt.Errorf("POST /session/%s/url -> HTTP %d: %s", sessionID, st, raw)
	}
	return nil
}

func wdAlive(base, sessionID string) bool {
	st, _, err := wdDo(http.MethodGet, wdBaseURL(base)+"/session/"+sessionID+"/url", nil)
	return err == nil && st >= 200 && st < 300
}

func wdDeleteSession(base, sessionID string) {
	if base == "" || sessionID == "" {
		return
	}
	_, _, _ = wdDo(http.MethodDelete, wdBaseURL(base)+"/session/"+sessionID, nil)
}

// wdEnsureSession returns a live ChromeDriver UUID. created is true only when
// this call issued POST /session (no existing window).
func wdEnsureSession(base, knownID string) (id string, created bool, err error) {
	base = wdBaseURL(base)
	if base == "" {
		return "", false, fmt.Errorf("webdriver url is empty")
	}
	if knownID != "" && wdAlive(base, knownID) {
		return knownID, false, nil
	}
	ids, listErr := wdListSessions(base)
	if listErr == nil {
		for _, sid := range ids {
			if sid != "" && wdAlive(base, sid) {
				return sid, false, nil
			}
		}
	}
	id, err = wdCreateSession(base)
	if err != nil {
		if listErr != nil {
			return "", false, fmt.Errorf("%v (list: %v)", err, listErr)
		}
		return "", false, err
	}
	return id, true, nil
}
