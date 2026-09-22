package app

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func TestSweepSubscribesNewAgentAndReceivesDone(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "finished")
	fh.subscriptions = make(chan []string, 10)
	sweep := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- d.run(ctx, sweep) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	waitSubscription := func() []string {
		t.Helper()
		select {
		case panes := <-fh.subscriptions:
			return panes
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for subscription")
			return nil
		}
	}
	flush := func() {
		t.Helper()
		ack := make(chan struct{})
		d.enqueue(func(context.Context) { close(ack) })
		select {
		case <-ack:
		case <-time.After(3 * time.Second):
			t.Fatal("daemon stopped processing updates")
		}
	}
	waitSubscription()

	// No lifecycle event arrives for this new pane: only the sweep
	// discovers it. The existing subscription must be rebuilt.
	fh.mu.Lock()
	fh.agents = append(fh.agents, domain.Agent{
		PaneID: "w1:p7", Kind: domain.KindPi, Status: domain.StatusWorking,
	})
	fh.mu.Unlock()
	sweep <- time.Now()
	if panes := waitSubscription(); !slices.Contains(panes, "w1:p7") {
		t.Fatalf("new agent not subscribed: %v", panes)
	}
	fh.events <- domain.Event{Kind: domain.EventAgentStatusChanged, PaneID: "w1:p7", Status: domain.StatusDone}
	flush()
	if sent := ft.sentLog(); len(sent) != 1 || sent[0].ThreadID != 102 {
		t.Fatalf("new agent completion not delivered: %+v", sent)
	}

	fh.mu.Lock()
	fh.agents[1].Status = domain.StatusDone
	fh.mu.Unlock()
	sweep <- time.Now()
	flush()
	select {
	case panes := <-fh.subscriptions:
		t.Fatalf("unchanged pane set unnecessarily resubscribed: %v", panes)
	default:
	}
	if got := len(ft.sentLog()); got != 1 {
		t.Fatalf("sweep duplicated completed notification: %d", got)
	}
}

func TestSweepRecoversMissedDoneAndAllowsNextIdenticalTurn(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "finished")
	ctx := context.Background()
	if err := d.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for turn := 1; turn <= 2; turn++ {
		fh.agents[0].Status = domain.StatusWorking
		if err := d.reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		fh.agents[0].Status = domain.StatusDone
		// The status event was missed, and two sweeps see the same done.
		for range 2 {
			if err := d.reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			fh.screen.Text += "\nterminal clock updated"
		}
		if got := len(ft.sentLog()); got != turn {
			t.Fatalf("turn %d: sent %d completion notices", turn, got)
		}
	}
}
