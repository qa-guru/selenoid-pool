package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Slot mirrors orchestrator/main.py Slot. Optional fields are pointers so that
// absent values serialize to JSON null (matching Python None).
type Slot struct {
	ID                      string
	Protocol                string
	Browser                 string
	Pool                    string
	WarmURL                 string
	SessionID               string
	WebdriverURL            *string
	WebdriverURLLoopback    *string
	PlaywrightWsURL         *string
	PlaywrightWsURLLoopback *string
	CdpURL                  *string
	CdpURLLoopback          *string
	ReservedBy              *string
	// ReservedAt / LeaseExpiresAt bound the reservation: the reaper frees the
	// slot once the lease deadline passes. Both are zero while the slot is free.
	ReservedAt     time.Time
	LeaseExpiresAt time.Time
	// StartedAt is the slot's current "birth": config load or last successful
	// recycle. SessionCount counts reserve/lease handouts since then.
	StartedAt    time.Time
	SessionCount int
	// Recycling is set while the container is being restarted. RecyclePending
	// marks a slot that crossed the recycle thresholds but could not be
	// restarted (no docker socket / restart failed) — it keeps serving.
	Recycling      bool
	RecyclePending bool
	// Container overrides the docker container name used for recycle restarts.
	// Empty → derived from the warm_url hostname when it is not loopback.
	Container string
	// LastRecycleAttempt throttles recycle retries after a failure.
	LastRecycleAttempt time.Time
	// DriverSessionID is the live ChromeDriver UUID (hot lease). Not SessionID
	// (stable slot / video prefix). Empty on warm slots and Playwright.
	DriverSessionID string
}

// slotPayload is the camelCase wire format returned by the orchestrator API.
type slotPayload struct {
	ID                      string     `json:"id"`
	Protocol                string     `json:"protocol"`
	Browser                 string     `json:"browser"`
	Pool                    string     `json:"pool"`
	SessionID               string     `json:"sessionId"`
	WarmURL                 string     `json:"warmUrl"`
	WebdriverURL            *string    `json:"webdriverUrl"`
	WebdriverURLLoopback    *string    `json:"webdriverUrlLoopback"`
	PlaywrightWsURL         *string    `json:"playwrightWsUrl"`
	PlaywrightWsURLLoopback *string    `json:"playwrightWsUrlLoopback"`
	CdpURL                  *string    `json:"cdpUrl"`
	CdpURLLoopback          *string    `json:"cdpUrlLoopback"`
	ReservedBy              *string    `json:"reservedBy"`
	ReservedAt              *time.Time `json:"reservedAt"`
	LeaseExpiresAt          *time.Time `json:"leaseExpiresAt"`
	SessionCount            int        `json:"sessionCount"`
	AgeSec                  int64      `json:"ageSec"`
	Recycling               bool       `json:"recycling"`
	RecyclePending          bool       `json:"recyclePending"`
	DriverSessionID         string     `json:"driverSessionId,omitempty"`
}

func (s *Slot) payload() slotPayload {
	return s.payloadFor(false)
}

func (s *Slot) payloadFor(loopback bool) slotPayload {
	wd := s.WebdriverURL
	if loopback && s.WebdriverURLLoopback != nil && strings.TrimSpace(*s.WebdriverURLLoopback) != "" {
		wd = s.WebdriverURLLoopback
	}
	ws := s.PlaywrightWsURL
	if loopback && s.PlaywrightWsURLLoopback != nil && strings.TrimSpace(*s.PlaywrightWsURLLoopback) != "" {
		ws = s.PlaywrightWsURLLoopback
	}
	cdp := s.CdpURL
	if loopback && s.CdpURLLoopback != nil && strings.TrimSpace(*s.CdpURLLoopback) != "" {
		cdp = s.CdpURLLoopback
	}
	return slotPayload{
		ID:                      s.ID,
		Protocol:                s.Protocol,
		Browser:                 s.Browser,
		Pool:                    s.Pool,
		SessionID:               s.SessionID,
		WarmURL:                 s.WarmURL,
		WebdriverURL:            wd,
		WebdriverURLLoopback:    s.WebdriverURLLoopback,
		PlaywrightWsURL:         ws,
		PlaywrightWsURLLoopback: s.PlaywrightWsURLLoopback,
		CdpURL:                  cdp,
		CdpURLLoopback:          s.CdpURLLoopback,
		ReservedBy:              s.ReservedBy,
		ReservedAt:              timePtr(s.ReservedAt),
		LeaseExpiresAt:          timePtr(s.LeaseExpiresAt),
		SessionCount:            s.SessionCount,
		AgeSec:                  s.ageSec(),
		Recycling:               s.Recycling,
		RecyclePending:          s.RecyclePending,
		DriverSessionID:         s.DriverSessionID,
	}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	out := t.UTC()
	return &out
}

// ageSec is seconds since the slot's current birth (load or last recycle).
func (s *Slot) ageSec() int64 {
	if s.StartedAt.IsZero() {
		return 0
	}
	sec := int64(time.Since(s.StartedAt).Seconds())
	if sec < 0 {
		return 0
	}
	return sec
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func (s *Slot) hasLoopbackWD() bool {
	if s.WebdriverURLLoopback != nil && isLoopbackURL(*s.WebdriverURLLoopback) {
		return true
	}
	if s.WebdriverURL != nil && isLoopbackURL(*s.WebdriverURL) {
		return true
	}
	return false
}

func (s *Slot) hasLoopbackPW() bool {
	if s.PlaywrightWsURLLoopback != nil && isLoopbackURL(*s.PlaywrightWsURLLoopback) {
		return true
	}
	if s.PlaywrightWsURL != nil && isLoopbackURL(*s.PlaywrightWsURL) {
		return true
	}
	return false
}

func (s *Slot) hasLoopbackCDP() bool {
	if s.CdpURLLoopback != nil && isLoopbackURL(*s.CdpURLLoopback) {
		return true
	}
	if s.CdpURL != nil && isLoopbackURL(*s.CdpURL) {
		return true
	}
	return false
}

func (s *Slot) hasLoopbackEndpoint() bool {
	if s.Protocol == "playwright" {
		return s.hasLoopbackPW()
	}
	if s.Protocol == "cdp" {
		return s.hasLoopbackCDP()
	}
	return s.hasLoopbackWD()
}

// Pool is a protocol-agnostic set of warm slots. A mutex guards mutations
// because Go's net/http serves requests concurrently (unlike the single
// process Flask dev server).
type Pool struct {
	mu    sync.Mutex
	slots []*Slot

	// LeaseTTL is the default reservation lifetime; MaxLeaseTTL clamps the
	// per-request ttlSec override. The reaper frees expired reservations.
	LeaseTTL    time.Duration
	MaxLeaseTTL time.Duration
	// RecycleMaxSessions / RecycleMaxAge rotate warm containers. Zero disables.
	RecycleMaxSessions int
	RecycleMaxAge      time.Duration
}

func (p *Pool) byID(id string) *Slot {
	for _, slot := range p.slots {
		if slot.ID == id {
			return slot
		}
	}
	return nil
}

func (s *Slot) isHot() bool {
	return strings.EqualFold(s.Pool, "hot")
}

// anonymousOwner is the ReservedBy value used when reserve/lease send no owner.
// Anonymous reservations may be released/renewed by anyone; a named owner must
// be echoed back (or bypassed with force).
const anonymousOwner = "anonymous"

// canRelease reports whether owner may free or renew this reservation.
// force is the documented operator escape hatch.
func (s *Slot) canRelease(owner string, force bool) bool {
	if s.ReservedBy == nil || force || *s.ReservedBy == anonymousOwner {
		return true
	}
	return owner != "" && owner == *s.ReservedBy
}

// claim marks a slot reserved for owner with the given lease TTL.
func (s *Slot) claim(owner string, ttl time.Duration) {
	now := time.Now()
	s.ReservedBy = &owner
	s.ReservedAt = now
	s.LeaseExpiresAt = now.Add(ttl)
	s.SessionCount++
}

// unclaim frees the slot. The session/recycle counters are kept.
func (s *Slot) unclaim() {
	s.ReservedBy = nil
	s.ReservedAt = time.Time{}
	s.LeaseExpiresAt = time.Time{}
}

// expired reports whether the reservation outlived its lease deadline.
// A zero deadline never expires (defensive — claim always sets one).
func (s *Slot) expired(now time.Time) bool {
	return s.ReservedBy != nil && !s.LeaseExpiresAt.IsZero() && now.After(s.LeaseExpiresAt)
}

// dueForRecycle reports whether a free slot crossed a rotation threshold.
// Hot slots rotate on age only: session count is their expected load, while
// the session age is what wedges the long-lived browser (input events stop
// reaching the page — seen on a 28h / 25-lease Chrome session).
func (s *Slot) dueForRecycle(maxSessions int, maxAge time.Duration, now time.Time) bool {
	if s.ReservedBy != nil || s.Recycling {
		return false
	}
	if maxAge > 0 && !s.StartedAt.IsZero() && now.Sub(s.StartedAt) >= maxAge {
		return true
	}
	if s.isHot() {
		return false
	}
	return maxSessions > 0 && s.SessionCount >= maxSessions
}

// containerName resolves the docker container to restart on recycle:
// the explicit `container` config field, else the warm_url hostname when it is
// a docker-DNS name (not loopback). Empty means "cannot restart by name".
func (s *Slot) containerName() string {
	if name := strings.TrimSpace(s.Container); name != "" {
		return name
	}
	u, err := url.Parse(s.WarmURL)
	if err != nil || u.Hostname() == "" || isLoopbackHost(u.Hostname()) {
		return ""
	}
	return u.Hostname()
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

// available returns unreserved warm slots, optionally filtered by protocol/browser.
// Empty filter strings mean "no filter", matching the Python truthiness check.
// needLoopback keeps only slots the host hub can dial (127.0.0.1 / localhost / ::1).
// Hot slots (pool=hot) are excluded — reserve them by slotId or POST /pool/lease.
func (p *Pool) available(protocol, browser string, needLoopback bool) []*Slot {
	return p.availableClass("warm", protocol, browser, needLoopback)
}

// availableClass is available() for one slot class ("warm" or "hot").
func (p *Pool) availableClass(class, protocol, browser string, needLoopback bool) []*Slot {
	wantHot := strings.EqualFold(class, "hot")
	var result []*Slot
	for _, slot := range p.slots {
		if slot.ReservedBy != nil || slot.Recycling {
			continue
		}
		if slot.isHot() != wantHot {
			continue
		}
		if protocol != "" && slot.Protocol != protocol {
			continue
		}
		if browser != "" && slot.Browser != browser {
			continue
		}
		if needLoopback && !slot.hasLoopbackEndpoint() {
			continue
		}
		result = append(result, slot)
	}
	return result
}

func (s *Slot) wdBase() string {
	return s.wdDialURL()
}

// wdDialURL is where this process talks to ChromeDriver (docker DNS on box1).
// Loopback is for hub/clients on the host — see payloadFor(loopback).
func (s *Slot) wdDialURL() string {
	if s.WebdriverURL != nil && strings.TrimSpace(*s.WebdriverURL) != "" {
		return strings.TrimRight(strings.TrimSpace(*s.WebdriverURL), "/")
	}
	if s.WebdriverURLLoopback != nil && strings.TrimSpace(*s.WebdriverURLLoopback) != "" {
		return strings.TrimRight(strings.TrimSpace(*s.WebdriverURLLoopback), "/")
	}
	return ""
}

type rawSlot struct {
	ID                      string  `yaml:"id"`
	Protocol                string  `yaml:"protocol"`
	Browser                 string  `yaml:"browser"`
	Pool                    string  `yaml:"pool"`
	WarmURL                 string  `yaml:"warm_url"`
	SessionID               string  `yaml:"session_id"`
	Container               string  `yaml:"container"`
	WebdriverURL            *string `yaml:"webdriver_url"`
	WebdriverURLLoopback    *string `yaml:"webdriver_url_loopback"`
	PlaywrightWsURL         *string `yaml:"playwright_ws_url"`
	PlaywrightWsURLLoopback *string `yaml:"playwright_ws_url_loopback"`
	CdpURL                  *string `yaml:"cdp_url"`
	CdpURLLoopback          *string `yaml:"cdp_url_loopback"`
}

type rawConfig struct {
	// LeaseTTL is the default reservation TTL ("1h"). MaxLeaseTTL clamps the
	// per-request ttlSec ("6h"). Recycle knobs rotate warm containers.
	LeaseTTL           string    `yaml:"lease_ttl"`
	MaxLeaseTTL        string    `yaml:"max_lease_ttl"`
	RecycleMaxSessions int       `yaml:"recycle_after_sessions"`
	RecycleMaxAge      string    `yaml:"recycle_after_age"`
	Slots              []rawSlot `yaml:"slots"`
}

func parseDur(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("duration %q: %v", raw, err)
	}
	return d, nil
}

// loadPool reads the YAML config and builds the pool, applying the same
// defaults as orchestrator/main.py::load_pool.
func loadPool(configPath string) (*Pool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	leaseTTL, err := parseDur(raw.LeaseTTL, time.Hour)
	if err != nil {
		return nil, fmt.Errorf("lease_ttl: %v", err)
	}
	maxLeaseTTL, err := parseDur(raw.MaxLeaseTTL, 6*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("max_lease_ttl: %v", err)
	}
	if leaseTTL <= 0 || maxLeaseTTL < leaseTTL {
		return nil, fmt.Errorf("lease_ttl must be > 0 and <= max_lease_ttl")
	}
	recycleMaxAge, err := parseDur(raw.RecycleMaxAge, 0)
	if err != nil {
		return nil, fmt.Errorf("recycle_after_age: %v", err)
	}

	now := time.Now()
	slots := make([]*Slot, 0, len(raw.Slots))
	for i, item := range raw.Slots {
		if item.ID == "" {
			return nil, fmt.Errorf("slot #%d: id is required", i)
		}
		if item.WarmURL == "" {
			return nil, fmt.Errorf("slot %q: warm_url is required", item.ID)
		}

		protocol := item.Protocol
		if protocol == "" {
			protocol = "webdriver"
		}
		browser := item.Browser
		if browser == "" {
			browser = "chrome"
		}
		sessionID := item.SessionID
		if sessionID == "" {
			sessionID = item.ID
		}
		poolName := strings.TrimSpace(item.Pool)
		if poolName == "" {
			poolName = "warm"
		}

		slots = append(slots, &Slot{
			ID:                      item.ID,
			Protocol:                protocol,
			Browser:                 browser,
			Pool:                    poolName,
			WarmURL:                 strings.TrimRight(item.WarmURL, "/"),
			SessionID:               sessionID,
			Container:               item.Container,
			StartedAt:               now,
			WebdriverURL:            item.WebdriverURL,
			WebdriverURLLoopback:    item.WebdriverURLLoopback,
			PlaywrightWsURL:         item.PlaywrightWsURL,
			PlaywrightWsURLLoopback: item.PlaywrightWsURLLoopback,
			CdpURL:                  item.CdpURL,
			CdpURLLoopback:          item.CdpURLLoopback,
		})
	}

	return &Pool{
		slots:              slots,
		LeaseTTL:           leaseTTL,
		MaxLeaseTTL:        maxLeaseTTL,
		RecycleMaxSessions: raw.RecycleMaxSessions,
		RecycleMaxAge:      recycleMaxAge,
	}, nil
}
