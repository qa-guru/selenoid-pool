package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Slot mirrors orchestrator/main.py Slot. Optional fields are pointers so that
// absent values serialize to JSON null (matching Python None).
type Slot struct {
	ID              string
	Protocol        string
	Browser         string
	WarmURL         string
	SessionID       string
	WebdriverURL    *string
	PlaywrightWsURL *string
	ReservedBy      *string
}

// slotPayload is the camelCase wire format returned by the orchestrator API.
type slotPayload struct {
	ID              string  `json:"id"`
	Protocol        string  `json:"protocol"`
	Browser         string  `json:"browser"`
	SessionID       string  `json:"sessionId"`
	WarmURL         string  `json:"warmUrl"`
	WebdriverURL    *string `json:"webdriverUrl"`
	PlaywrightWsURL *string `json:"playwrightWsUrl"`
	ReservedBy      *string `json:"reservedBy"`
}

func (s *Slot) payload() slotPayload {
	return slotPayload{
		ID:              s.ID,
		Protocol:        s.Protocol,
		Browser:         s.Browser,
		SessionID:       s.SessionID,
		WarmURL:         s.WarmURL,
		WebdriverURL:    s.WebdriverURL,
		PlaywrightWsURL: s.PlaywrightWsURL,
		ReservedBy:      s.ReservedBy,
	}
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

// available returns unreserved slots, optionally filtered by protocol/browser.
// Empty filter strings mean "no filter", matching the Python truthiness check.
func (p *Pool) available(protocol, browser string) []*Slot {
	var result []*Slot
	for _, slot := range p.slots {
		if slot.ReservedBy != nil {
			continue
		}
		if protocol != "" && slot.Protocol != protocol {
			continue
		}
		if browser != "" && slot.Browser != browser {
			continue
		}
		result = append(result, slot)
	}
	return result
}

type rawSlot struct {
	ID              string  `yaml:"id"`
	Protocol        string  `yaml:"protocol"`
	Browser         string  `yaml:"browser"`
	WarmURL         string  `yaml:"warm_url"`
	SessionID       string  `yaml:"session_id"`
	WebdriverURL    *string `yaml:"webdriver_url"`
	PlaywrightWsURL *string `yaml:"playwright_ws_url"`
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

		slots = append(slots, &Slot{
			ID:              item.ID,
			Protocol:        protocol,
			Browser:         browser,
			WarmURL:         strings.TrimRight(item.WarmURL, "/"),
			SessionID:       sessionID,
			WebdriverURL:    item.WebdriverURL,
			PlaywrightWsURL: item.PlaywrightWsURL,
		})
	}

	return &Pool{slots: slots}, nil
}
