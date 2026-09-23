package herdr

import (
	"encoding/json"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func TestTranslateAgentRelease(t *testing.T) {
	for _, tc := range []struct {
		name     string
		data     string
		released bool
	}{
		{"released", `{"pane_id":"p1","workspace_id":"w1","agent":null,"released":true}`, true},
		{"released with final status", `{"pane_id":"p1","workspace_id":"w1","agent":"pi","released":true,"final_status":"done"}`, true},
		{"detected", `{"pane_id":"p1","workspace_id":"w1","agent":"pi","released":false,"final_status":"idle"}`, false},
		{"missing release flag", `{"pane_id":"p1","workspace_id":"w1","agent":null}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGateway("unused", nil)
			ev, ok := g.translate(eventEnvelope{
				Event: string(domain.EventAgentDetected), Data: json.RawMessage(tc.data),
			})
			if !ok || ev.Kind != domain.EventAgentDetected || ev.PaneID != "p1" || ev.WorkspaceID != "w1" || ev.AgentReleased != tc.released {
				t.Fatalf("translated event: %+v, ok=%v", ev, ok)
			}
		})
	}
}

func TestTranslateLiveLifecycleEvents(t *testing.T) {
	// Captured from Herdr 0.9.1: subscription names use dots, but the
	// lifecycle event envelopes use underscores. Status events use dots.
	for _, tc := range []struct {
		name     string
		data     string
		kind     domain.EventKind
		released bool
	}{
		{"pane_agent_detected", `{"agent":"pi","final_status":"idle","pane_id":"w6B:p6","released":true,"type":"pane_agent_detected","workspace_id":"w6B"}`, domain.EventAgentDetected, true},
		{"pane_closed", `{"pane_id":"w6B:p6","type":"pane_closed","workspace_id":"w6B"}`, domain.EventPaneClosed, false},
		{"pane.agent_status_changed", `{"agent_status":"unknown","pane_id":"w6B:p6","workspace_id":"w6B"}`, domain.EventAgentStatusChanged, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGateway("unused", nil)
			ev, ok := g.translate(eventEnvelope{Event: tc.name, Data: json.RawMessage(tc.data)})
			if !ok || ev.Kind != tc.kind || ev.PaneID != "w6B:p6" || ev.AgentReleased != tc.released {
				t.Fatalf("live event not translated: %+v, ok=%v", ev, ok)
			}
		})
	}
}
