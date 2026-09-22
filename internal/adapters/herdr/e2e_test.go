package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// TestE2EBlockedFromPi is the end-to-end proof of the whole chain:
//
//	pi shows a blocking prompt
//	  -> this repo's emitter publishes herdr:blocked
//	    -> Herdr's pi integration reports pane.report_agent{state:blocked}
//	      -> Herdr emits pane.agent_status_changed with status "blocked"
//	        -> this plugin's subscription sees it
//	          -> the screen read yields a dialog this plugin can turn into buttons
//
// It is opt-in because it mutates the machine: it creates a tab, starts a real
// pi agent, installs a temporary pi extension and closes the pane afterwards.
//
//	python3 - > /tmp/nothing <<'EOF'
//	EOF
//	make e2e          # wraps the env vars below
//
// Required env:
//
//	HERDR_E2E=1              run it at all
//	HERDR_E2E_FIXTURE        path to pi-extension/testfixture/herdr-selftest.ts
//	HERDR_E2E_WORKSPACE      workspace label to create the tab in (e.g. "task")
func TestE2EBlockedFromPi(t *testing.T) {
	if os.Getenv("HERDR_E2E") != "1" {
		t.Skip("set HERDR_E2E=1 to run the mutating end-to-end test")
	}
	fixture := os.Getenv("HERDR_E2E_FIXTURE")
	if fixture == "" {
		t.Fatal("HERDR_E2E_FIXTURE must point at herdr-selftest.ts")
	}
	workspaceLabel := os.Getenv("HERDR_E2E_WORKSPACE")
	if workspaceLabel == "" {
		t.Fatal("HERDR_E2E_WORKSPACE must name a workspace")
	}

	g, ctx := liveGateway(t)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	// 1. Install a uniquely named fixture so the test cannot overwrite or
	//    remove a user's extension.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	extDir := filepath.Join(home, ".pi", "agent", "extensions")
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("create %s: %v", extDir, err)
	}
	tmp, err := os.CreateTemp(extDir, "zz-herdr-selftest-*.ts")
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	installed := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		t.Fatalf("install fixture: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	defer func() {
		if err := os.Remove(installed); err != nil {
			t.Errorf("remove fixture %s: %v", installed, err)
		} else {
			t.Logf("removed fixture %s", installed)
		}
	}()
	t.Logf("installed fixture %s", installed)

	// 2. Find the workspace and open an unfocused tab in it.
	workspaces, err := g.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("workspace.list: %v", err)
	}
	var wsID string
	for _, w := range workspaces {
		if strings.EqualFold(w.Label, workspaceLabel) {
			wsID = w.ID
			break
		}
	}
	if wsID == "" {
		t.Fatalf("no workspace labelled %q", workspaceLabel)
	}
	tab, err := g.CreateTab(ctx, wsID)
	if err != nil {
		t.Fatalf("tab.create: %v", err)
	}
	t.Logf("created tab %s root pane %s", tab.ID, tab.RootPaneID)
	closed := false
	defer func() {
		if closed {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := g.ClosePane(cleanupCtx, tab.RootPaneID); err != nil {
			t.Errorf("cleanup pane.close %s: %v", tab.RootPaneID, err)
		} else {
			t.Logf("closed pane %s", tab.RootPaneID)
		}
	}()

	// 3. Start pi in it. agent.start waits for Herdr's detection to settle.
	agent, err := g.StartAgent(ctx, "herdr-selftest", "pi", tab.RootPaneID, 90*time.Second)
	if err != nil {
		t.Fatalf("agent.start: %v", err)
	}
	t.Logf("started %s kind=%s status=%s", agent.PaneID, agent.Kind, agent.Status)

	// 4. Subscribe before triggering, so no transition can be missed.
	events, err := g.Subscribe(ctx, []string{tab.RootPaneID})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// 5. Give the session a moment to finish loading extensions, then run the
	//    fixture command. agent.prompt types the text and submits it.
	time.Sleep(6 * time.Second)
	if err := g.Prompt(ctx, tab.RootPaneID, "/herdr-selftest"); err != nil {
		t.Fatalf("prompt the fixture command: %v", err)
	}
	t.Log("sent /herdr-selftest; waiting for blocked")

	// 6. Wait for the blocked transition. This is the assertion the whole test
	//    exists for: without the emitter this never arrives.
	var blockedSeen bool
	deadline := time.After(60 * time.Second)
	for !blockedSeen {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before blocked arrived")
			}
			t.Logf("event %s status=%s", ev.Kind, ev.Status)
			if ev.Kind == domain.EventAgentStatusChanged && ev.PaneID == tab.RootPaneID && ev.Status == domain.StatusBlocked {
				blockedSeen = true
			}
		case <-deadline:
			dumpState(t, ctx, g, tab.RootPaneID)
			t.Fatal("never saw blocked: the emitter, Herdr's consumer, or the fixture command did not fire")
		}
	}
	t.Log("BLOCKED observed via the event stream")

	// 7. Confirm agent.list agrees. Herdr 0.9.1 does not expose the pi
	//    report message here, so the question still comes from the screen.
	agents, err := g.ListAgents(ctx)
	if err != nil {
		t.Fatalf("agent.list: %v", err)
	}
	var listed *domain.Agent
	for i := range agents {
		if agents[i].PaneID == tab.RootPaneID {
			listed = &agents[i]
			break
		}
	}
	if listed == nil {
		t.Fatal("pane missing from agent.list")
	}
	t.Logf("agent.list says status=%s blocked reason=%q labels=%v title=%q display=%q",
		listed.Status, listed.BlockedReason(), listed.StateLabels, listed.Title, listed.DisplayAgent)
	if listed.Status != domain.StatusBlocked {
		t.Errorf("agent.list status = %s, want blocked", listed.Status)
	}

	// 8. The dialog must be readable, because that is what becomes the buttons.
	screen, src, dialog, err := g.ReadForDialog(ctx, tab.RootPaneID, listed.Kind, 60)
	if err != nil {
		t.Fatalf("read for dialog: %v", err)
	}
	t.Logf("screen source=%s bytes=%d dialog style=%s title=%q choices=%d",
		src, len(screen.Text), dialog.Style, dialog.Title, len(dialog.Choices))
	for _, c := range dialog.Choices {
		t.Logf("  button [%s] %-10s keys=%v", c.Key, domain.CutLabel(c.Label, 48), dialog.KeysFor(c))
	}
	if !dialog.Usable() {
		t.Fatalf("no usable dialog on a blocked pi pane; buttons would be missing.\nscreen tail:\n%s",
			tail(screen.Text, 25))
	}
	if dialog.Title != "Herdr bridge self-test · Continue with the bridge test?" || len(dialog.Choices) != 2 || dialog.Choices[0].Label != "Yes" || dialog.Choices[1].Label != "No" {
		t.Fatalf("unexpected dialog: %+v", dialog)
	}
	keys := dialog.KeysFor(dialog.Choices[0])
	if len(keys) != 1 || keys[0] != domain.KeyEnter {
		t.Fatalf("Yes keys = %v, want [enter]", keys)
	}

	// 9. Choose Yes, then verify the pane leaves blocked.
	if err := g.SendKeys(ctx, tab.RootPaneID, keys); err != nil {
		t.Fatalf("answer Yes: %v", err)
	}
	leftBlocked := false
	deadline = time.After(45 * time.Second)
	for !leftBlocked {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed")
			}
			t.Logf("event %s status=%s", ev.Kind, ev.Status)
			if ev.Kind == domain.EventAgentStatusChanged && ev.PaneID == tab.RootPaneID && ev.Status == domain.StatusIdle {
				leftBlocked = true
			}
		case <-deadline:
			t.Fatal("pane never reached idle after answering Yes: the emitter may not have published active:false")
		}
	}
	if err := g.ClosePane(ctx, tab.RootPaneID); err != nil {
		t.Errorf("pane.close: %v", err)
	} else {
		closed = true
		t.Logf("closed pane %s", tab.RootPaneID)
	}
}

// dumpState explains a failed run: the pane's status and screen are usually
// enough to tell "the emitter never fired" from "Herdr reported blocked but the
// dialog was unreadable".
func dumpState(t *testing.T, ctx context.Context, g *Gateway, paneID string) {
	t.Helper()
	if agents, err := g.ListAgents(ctx); err == nil {
		for _, a := range agents {
			if a.PaneID == paneID {
				t.Logf("agent.list: status=%s labels=%v display=%q", a.Status, a.StateLabels, a.DisplayAgent)
			}
		}
	} else {
		t.Logf("agent.list failed: %v", err)
	}
	screen, err := g.ReadScreen(ctx, paneID, domain.ScreenVisible, 60)
	if err != nil {
		t.Logf("screen read failed: %v", err)
		return
	}
	t.Logf("screen tail:\n%s", tail(screen.Text, 20))
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
