package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

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
	// DriverSessionID is the live ChromeDriver UUID (hot lease). Not SessionID
	// (stable slot / video prefix). Empty on warm slots and Playwright.
	DriverSessionID string
}

// slotPayload is the camelCase wire format returned by the orchestrator API.
type slotPayload struct {
	ID                      string  `json:"id"`
	Protocol                string  `json:"protocol"`
	Browser                 string  `json:"browser"`
	Pool                    string  `json:"pool"`
	SessionID               string  `json:"sessionId"`
	WarmURL                 string  `json:"warmUrl"`
	WebdriverURL            *string `json:"webdriverUrl"`
	WebdriverURLLoopback    *string `json:"webdriverUrlLoopback"`
	PlaywrightWsURL         *string `json:"playwrightWsUrl"`
	PlaywrightWsURLLoopback *string `json:"playwrightWsUrlLoopback"`
	CdpURL                  *string `json:"cdpUrl"`
	CdpURLLoopback          *string `json:"cdpUrlLoopback"`
	ReservedBy              *string `json:"reservedBy"`
	DriverSessionID         string  `json:"driverSessionId,omitempty"`
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
		DriverSessionID:         s.DriverSessionID,
	}
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
		if slot.ReservedBy != nil {
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
	WebdriverURL            *string `yaml:"webdriver_url"`
	WebdriverURLLoopback    *string `yaml:"webdriver_url_loopback"`
	PlaywrightWsURL         *string `yaml:"playwright_ws_url"`
	PlaywrightWsURLLoopback *string `yaml:"playwright_ws_url_loopback"`
	CdpURL                  *string `yaml:"cdp_url"`
	CdpURLLoopback          *string `yaml:"cdp_url_loopback"`
}

type rawConfig struct {
	Slots []rawSlot `yaml:"slots"`
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
			WebdriverURL:            item.WebdriverURL,
			WebdriverURLLoopback:    item.WebdriverURLLoopback,
			PlaywrightWsURL:         item.PlaywrightWsURL,
			PlaywrightWsURLLoopback: item.PlaywrightWsURLLoopback,
			CdpURL:                  item.CdpURL,
			CdpURLLoopback:          item.CdpURLLoopback,
		})
	}

	return &Pool{slots: slots}, nil
}
