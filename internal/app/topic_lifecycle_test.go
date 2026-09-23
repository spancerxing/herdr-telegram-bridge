package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func TestTopicDeletionTriggers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status domain.Status
		closed bool
		gone   bool
	}{
		{name: "done remains listed", status: domain.StatusDone},
		{name: "idle remains listed", status: domain.StatusIdle},
		{name: "unknown remains listed", status: domain.StatusUnknown},
		{name: "pane closed event", closed: true},
		{name: "agent disappears without event", gone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "finished")
			if err := d.reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if tc.closed {
				d.handleEvent(ctx, domain.Event{Kind: domain.EventPaneClosed, PaneID: "w1:p1"})
			} else {
				if tc.gone {
					fh.agents = nil
				} else {
					fh.agents[0].Status = tc.status
					d.handleEvent(ctx, domain.Event{Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: tc.status})
				}
				if err := d.reconcile(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if tc.closed || tc.gone {
				if len(ft.deleted) != 1 || ft.deleted[0] != 101 {
					t.Fatalf("topic deletion calls: %v", ft.deleted)
				}
				if _, exists := d.mapping.Topics["w1:p1"]; exists {
					t.Fatal("deleted topic still mapped")
				}
			} else if len(ft.deleted) != 0 || len(ft.closed) != 0 {
				t.Fatalf("listed agent topic removed: deleted=%v closed=%v", ft.deleted, ft.closed)
			}
		})
	}
}

func TestCleanupAlreadyDeletedTopic(t *testing.T) {
	for _, trigger := range []string{"sweep", "pane_closed", "agent_released"} {
		t.Run(trigger, func(t *testing.T) {
			ctx := context.Background()
			d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "")
			if err := d.reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			fh.agents = nil
			ft.deleteTopicErr = errors.New("telegram: topic gone: telegram api 400: Bad Request: TOPIC_ID_INVALID")
			switch trigger {
			case "sweep":
				if err := d.reconcile(ctx); err != nil {
					t.Fatal(err)
				}
			case "pane_closed":
				d.handleEvent(ctx, domain.Event{Kind: domain.EventPaneClosed, PaneID: "w1:p1"})
			case "agent_released":
				d.handleEvent(ctx, domain.Event{Kind: domain.EventAgentDetected, PaneID: "w1:p1", AgentReleased: true})
			}
			if _, exists := d.store.Load().Topics["w1:p1"]; exists || len(ft.closed) != 0 {
				t.Fatalf("absent topic retained or closed again: mapped=%v close=%v", exists, ft.closed)
			}
		})
	}
}

func TestFailedTopicCleanupRetries(t *testing.T) {
	for _, trigger := range []string{"sweep", "pane_closed", "agent_released"} {
		for _, recovery := range []string{"delete", "close"} {
			name := trigger + "/" + recovery
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "")
				if err := d.reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				fh.agents = nil
				ft.deleteTopicErr = context.DeadlineExceeded
				ft.closeTopicErr = context.DeadlineExceeded
				if trigger == "pane_closed" {
					d.handleEvent(ctx, domain.Event{Kind: domain.EventPaneClosed, PaneID: "w1:p1"})
				} else if trigger == "agent_released" {
					d.handleEvent(ctx, domain.Event{Kind: domain.EventAgentDetected, PaneID: "w1:p1", AgentReleased: true})
				} else if err := d.reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				// Reload the persisted mapping: retries must survive a restart.
				d.mapping = d.store.Load()
				if e := d.mapping.Topics["w1:p1"]; e == nil || e.Closed {
					t.Fatalf("failed cleanup must remain pending: %+v", e)
				}
				if recovery == "delete" {
					ft.deleteTopicErr = nil
				} else {
					ft.closeTopicErr = nil
				}
				if err := d.reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				e := d.store.Load().Topics["w1:p1"]
				if recovery == "delete" {
					if e != nil || len(ft.deleted) != 1 || ft.deleted[0] != 101 {
						t.Fatalf("delete not retried: entry=%+v deleted=%v", e, ft.deleted)
					}
				} else if e == nil || !e.Closed || len(ft.closed) != 2 || ft.closed[1] != 101 {
					t.Fatalf("close not retried: entry=%+v closed=%v", e, ft.closed)
				}
			})
		}
	}
}

func TestAgentReleaseDeletesTopicWithoutSweep(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "")
	fh.subscriptions = make(chan []string, 10)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	// A nil sweep channel disables polling: only the event can delete.
	go func() { finished <- d.run(ctx, nil) }()
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
	select {
	case <-fh.subscriptions:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not subscribe")
	}
	fh.mu.Lock()
	fh.agents = nil
	// Release must still clean up if the subsequent list refresh fails.
	fh.listErr = context.DeadlineExceeded
	fh.mu.Unlock()
	select {
	case fh.events <- domain.Event{Kind: domain.EventAgentDetected, PaneID: "w1:p1", AgentReleased: true}:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not receive release")
	}
	ack := make(chan struct{})
	d.enqueue(func(context.Context) { close(ack) })
	select {
	case <-ack:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not process release")
	}
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if len(ft.deleted) != 1 || ft.deleted[0] != 101 {
		t.Fatalf("release did not delete topic without polling: %v", ft.deleted)
	}
}

func TestAgentCanReturnAfterRelease(t *testing.T) {
	for _, closeOnly := range []bool{false, true} {
		name := "deleted"
		if closeOnly {
			name = "closed"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "")
			if err := d.reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if closeOnly {
				ft.deleteTopicErr = context.DeadlineExceeded
			}
			d.questions["w1:p1"] = &question{messageID: 1}
			d.lastPosted["w1:p1"] = "done"
			d.typing["w1:p1"] = typingWait{}
			d.handleEvent(ctx, domain.Event{Kind: domain.EventAgentDetected, PaneID: "w1:p1", AgentReleased: true})
			if len(d.agents) != 0 || len(d.questions) != 0 || len(d.typing) != 0 || len(d.lastPosted) != 0 {
				t.Fatal("released agent retained runtime state")
			}
			fh.agents = nil
			// A duplicate release must not create another topic.
			d.handleEvent(ctx, domain.Event{Kind: domain.EventAgentDetected, PaneID: "w1:p1", AgentReleased: true})
			fh.agents = agentsFixture()[:1]
			d.handleEvent(ctx, domain.Event{Kind: domain.EventAgentDetected, PaneID: "w1:p1"})
			thread, ok := d.mapping.ThreadFor("w1:p1")
			if !ok {
				t.Fatal("returning agent has no open topic")
			}
			if closeOnly {
				if thread != 101 || len(ft.reopened) != 1 {
					t.Fatalf("closed topic not reopened: thread=%d reopened=%v", thread, ft.reopened)
				}
			} else if thread != 102 || len(ft.created) != 2 {
				t.Fatalf("deleted topic not replaced: thread=%d created=%v", thread, ft.created)
			}
		})
	}
}
