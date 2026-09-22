package herdr

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// These tests talk to the Herdr instance actually running on this machine.
// They skip when there is no socket, so `go test ./...` stays green on a
// machine without Herdr, and they are the only proof that the wire types in
// wire.go match the server rather than the schema alone.

func liveGateway(t *testing.T) (*Gateway, context.Context) {
	t.Helper()
	path := DefaultSocketPath()
	if path == "" {
		t.Skip("no HERDR_SOCKET_PATH and no home directory")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no herdr socket at %s: %v", path, err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return NewGateway(path, log), context.Background()
}

func TestLivePing(t *testing.T) {
	g, ctx := liveGateway(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	info, err := g.Ping(ctx)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Logf("herdr %s protocol %d capabilities=%v", info.Version, info.Protocol, info.Capabilities)
	if info.Protocol == 0 {
		t.Error("protocol is 0; pong was not decoded")
	}
	// The build targets a specific protocol; a difference is a warning at
	// runtime but worth surfacing loudly in a test run.
	if info.Protocol != ProtocolVersion {
		t.Logf("NOTE: server protocol %d, this build targets %d", info.Protocol, ProtocolVersion)
	}
}

func TestLiveListAgents(t *testing.T) {
	g, ctx := liveGateway(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	agents, err := g.ListAgents(ctx)
	if err != nil {
		t.Fatalf("agent.list: %v", err)
	}
	if len(agents) == 0 {
		t.Skip("no agents running")
	}
	for _, a := range agents {
		t.Logf("pane=%s terminal=%s raw=%q kind=%q status=%s name=%q display=%q title=%q cwd=%s labels=%v",
			a.PaneID, a.TerminalID, a.RawKind, a.Kind, a.Status,
			a.Name, a.DisplayAgent, a.TerminalTitle, a.Cwd, a.StateLabels)
		if a.PaneID == "" {
			t.Error("agent with empty pane id")
		}
		if a.Kind == "" {
			t.Logf("NOTE: kind %q is not one of the four this plugin profiles", a.RawKind)
		}
	}
}

func TestLiveReadScreenSources(t *testing.T) {
	g, ctx := liveGateway(t)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	agents, err := g.ListAgents(ctx)
	if err != nil {
		t.Fatalf("agent.list: %v", err)
	}
	if len(agents) == 0 {
		t.Skip("no agents running")
	}
	target := agents[0]

	// Every read source must either answer or fail with a code the caller
	// can act on. The point of this test is to record which sources work on
	// the live protocol, because "recent" is refused for a working
	// alternate-screen pane and that is what broke the previous plugin.
	for _, src := range []domain.ScreenSource{
		domain.ScreenVisible,
		domain.ScreenDetection,
		domain.ScreenRecent,
		domain.ScreenRecentUnwrapped,
	} {
		screen, err := g.ReadScreen(ctx, target.PaneID, src, 40)
		if err != nil {
			var api *APIError
			code := "?"
			if errors.As(err, &api) {
				code = api.Code
			}
			t.Logf("source=%-18s FAILED code=%s err=%v", src, code, err)
			continue
		}
		n := lines(screen.Text)
		t.Logf("source=%-18s ok: %d lines, %d bytes, revision=%d truncated=%v",
			src, n, len(screen.Text), screen.Revision, screen.Truncated)
	}
}

func TestLiveSubscribe(t *testing.T) {
	g, ctx := liveGateway(t)
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	agents, err := g.ListAgents(ctx)
	if err != nil {
		t.Fatalf("agent.list: %v", err)
	}
	if len(agents) == 0 {
		t.Skip("no agents running")
	}
	panes := make([]string, 0, len(agents))
	for _, a := range agents {
		panes = append(panes, a.PaneID)
	}
	t.Logf("subscribing to %v", panes)

	ch, err := g.Subscribe(ctx, panes)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// The server pushes the current status of each subscribed pane as soon
	// as the subscription starts, so at least one event is expected. A
	// quiet few seconds with no event is reported, not failed, because an
	// idle machine legitimately produces none.
	deadline := time.After(8 * time.Second)
	seen := 0
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if seen == 0 {
					t.Fatal("subscription channel closed before any event")
				}
				return
			}
			seen++
			t.Logf("event=%s pane=%s workspace=%s agent=%q status=%s hasStatus=%v",
				ev.Kind, ev.PaneID, ev.WorkspaceID, ev.AgentName, ev.Status, ev.HasStatus)
			if seen >= 5 {
				return
			}
		case <-deadline:
			if seen == 0 {
				t.Log("no events in 8s; the subscription is open but the machine was quiet")
			}
			return
		}
	}
}

func TestLiveIntegrationList(t *testing.T) {
	g, ctx := liveGateway(t)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	list, err := g.IntegrationList(ctx)
	if err != nil {
		t.Fatalf("integration.list: %v", err)
	}
	// Keyed by the socket API's target spelling, which uses an underscore
	// for Antigravity even though the CLI takes a hyphen.
	wanted := map[string]bool{"claude": false, "codex": false, "antigravity_cli": false, "pi": false}
	for _, i := range list {
		if _, ok := wanted[i.Target]; ok {
			wanted[i.Target] = true
			t.Logf("integration %-16s installed=%-5v outdated=%-5v path=%s",
				i.Target, i.Installed, i.Outdated, i.Path)
		}
	}
	for target, found := range wanted {
		if !found {
			t.Errorf("integration.list did not mention %s", target)
		}
	}
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
