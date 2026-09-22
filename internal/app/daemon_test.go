package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// piCursorDialog is the dialog shape the e2e test measures on a real pi:
// the cursor rests on Yes, so Yes is answered with enter alone.
func piCursorDialog() domain.Dialog {
	return domain.Dialog{
		Kind:  domain.KindPi,
		Style: domain.StyleCursor,
		Title: "Continue with the bridge test?",
		Choices: []domain.Choice{
			{Number: 1, Label: "Yes"},
			{Number: 2, Label: "No"},
		},
		Cursor: 1,
	}
}

func newTestDaemon(t *testing.T, agents []domain.Agent, dg domain.Dialog, screen string) (*Daemon, *fakeHerdr, *fakeTelegram) {
	t.Helper()
	fh := &fakeHerdr{agents: agents, dialog: dg, screen: domain.Screen{Text: screen}, events: make(chan domain.Event)}
	ft := &fakeTelegram{icons: map[domain.Status]domain.TopicIcon{
		domain.StatusWorking: {CustomEmojiID: "w"},
		domain.StatusIdle:    {CustomEmojiID: "i"},
		domain.StatusBlocked: {CustomEmojiID: "b"},
		domain.StatusDone:    {CustomEmojiID: "d"},
		domain.StatusExited:  {CustomEmojiID: "x"},
	}}
	store := NewMappingStore(t.TempDir(), testLog())
	d := NewDaemon(fh, ft, store, -100, []domain.Operator{{ID: 7}}, RealClock{}, testLog())
	// In tests, assume away from desk by default so notifications ring.
	d.SetIdleChecker(fakeIdleChecker{idle: 10 * time.Minute})
	// Dashboard-specific tests start from an empty mapping; these focused
	// topic tests keep their message-count assertions about questions only.
	d.mapping.DashboardMessageID = 999
	d.mapping.DashboardPinned = true
	return d, fh, ft
}

func agentsFixture() []domain.Agent {
	return []domain.Agent{
		{PaneID: "w1:p1", Kind: domain.KindPi, DisplayAgent: "π - task", Status: domain.StatusWorking},
		{PaneID: "w1:p2", Kind: domain.KindClaude, DisplayAgent: "fix-thing", Status: domain.StatusIdle},
	}
}

func TestDashboardIsCreatedAndPinned(t *testing.T) {
	d, _, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	d.mapping.DashboardMessageID = 0
	d.mapping.DashboardPinned = false

	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	sent := ft.sentLog()
	if len(sent) != 1 || sent[0].ThreadID != 0 {
		t.Fatalf("dashboard post: %+v", sent)
	}
	if !strings.Contains(sent[0].Text, "📊 Herdr agents") || !strings.Contains(sent[0].Text, "π - task") {
		t.Fatalf("dashboard text: %q", sent[0].Text)
	}
	if d.mapping.DashboardMessageID != 1 || !d.mapping.DashboardPinned {
		t.Fatalf("dashboard mapping: %+v", d.mapping)
	}
	if len(ft.pinned) != 1 || ft.pinned[0] != 1 {
		t.Fatalf("dashboard pin calls: %v", ft.pinned)
	}

	d.handleStatus(context.Background(), domain.Event{
		Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusBlocked,
	})
	if len(ft.editedText) != 1 || !strings.Contains(ft.editedText[0].text, "❓ π - task — blocked") {
		t.Fatalf("dashboard update: %+v", ft.editedText)
	}
}

func TestReconcileCreatesTopicsAndDeletesOrphans(t *testing.T) {
	d, _, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	// A pane from a previous run whose agent is gone.
	d.mapping.Link("w1:p9", &Entry{ThreadID: 5, Name: "gone", Status: domain.StatusIdle})

	if err := d.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := len(ft.created); got != 2 {
		t.Fatalf("created %d topics, want 2: %+v", got, ft.created)
	}
	if ft.created[0].name != "π - task" || ft.created[0].icon.CustomEmojiID != "w" {
		t.Fatalf("first topic: %+v", ft.created[0])
	}
	if len(ft.deleted) != 1 || ft.deleted[0] != 5 {
		t.Fatalf("orphan delete: %v", ft.deleted)
	}
	if _, exists := d.mapping.Topics["w1:p9"]; exists {
		t.Fatal("orphan entry should be removed from mapping after delete")
	}
	if thread, ok := d.mapping.ThreadFor("w1:p1"); !ok || thread != 101 {
		t.Fatalf("w1:p1 thread = %d %v", thread, ok)
	}
}

func TestReconcilePostsBlockedQuestion(t *testing.T) {
	agents := agentsFixture()
	agents[0].Status = domain.StatusBlocked
	d, _, ft := newTestDaemon(t, agents, piCursorDialog(), "screen body")

	if err := d.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	sent := ft.sentLog()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want the blocked question", len(sent))
	}
	out := sent[0]
	if !out.Notify {
		t.Error("a question must ring")
	}
	if out.ThreadID != 101 {
		t.Errorf("question thread = %d, want 101", out.ThreadID)
	}
	if out.Text != "❓ screen body" {
		t.Errorf("question text = %q", out.Text)
	}
	if len(out.Buttons) != 2 || out.Buttons[0].Text != "Yes" || out.Buttons[0].Data != "1" {
		t.Errorf("buttons: %+v", out.Buttons)
	}
	if q := d.questions["w1:p1"]; q == nil || q.messageID != 1 {
		t.Errorf("question not recorded: %+v", q)
	}
}

func TestStatusEventFlipsIconAndRetires(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture(), piCursorDialog(), "screen")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	// blocked: icon flips and the question is posted
	d.handleStatus(context.Background(), domain.Event{
		Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusBlocked,
	})
	if len(ft.edited) == 0 || ft.edited[len(ft.edited)-1].icon.CustomEmojiID != "b" {
		t.Fatalf("icon edits: %+v", ft.edited)
	}
	if len(ft.sentLog()) != 1 {
		t.Fatalf("question not posted on blocked")
	}

	// idle again: the keyboard is retired
	d.handleStatus(context.Background(), domain.Event{
		Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusIdle,
	})
	kb := ft.keyboardLog()
	if len(kb) == 0 || len(kb[len(kb)-1].buttons) != 0 {
		t.Fatalf("keyboard not retired: %+v", kb)
	}
	if _, open := d.questions["w1:p1"]; open {
		t.Fatal("question still open after idle")
	}
	if fh.keyLog() != nil {
		t.Fatalf("no keys should be sent on a status flip: %v", fh.keyLog())
	}
}

func TestPaneClosedDeletesTopic(t *testing.T) {
	d, _, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.questions["w1:p1"] = &question{messageID: 1, dialog: piCursorDialog()}

	d.handleClosed(context.Background(), "w1:p1")
	if len(ft.deleted) != 1 || ft.deleted[0] != 101 {
		t.Fatalf("deleted: %v", ft.deleted)
	}
	if _, exists := d.mapping.Topics["w1:p1"]; exists {
		t.Fatal("entry not removed after delete")
	}
	if _, open := d.questions["w1:p1"]; open {
		t.Fatal("question not dropped on close")
	}
	if _, has := d.agents["w1:p1"]; has {
		t.Fatal("agent not dropped on close")
	}
}

func TestPaneClosedFallbackToCloseWhenDeleteFails(t *testing.T) {
	d, _, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	ft.deleteTopicErr = context.DeadlineExceeded

	d.handleClosed(context.Background(), "w1:p1")
	if len(ft.closed) != 1 || ft.closed[0] != 101 {
		t.Fatalf("fallback close: %v", ft.closed)
	}
	if e := d.mapping.Topics["w1:p1"]; !e.Closed {
		t.Fatalf("entry not marked closed: %+v", e)
	}
}

func TestStatusDonePostsNotification(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	fh.screen = domain.Screen{Text: "All 5 tests passed"}

	d.handleStatus(context.Background(), domain.Event{
		Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusDone,
	})
	sent := ft.sentLog()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1 done post", len(sent))
	}
	out := sent[0]
	if out.ThreadID != 101 {
		t.Errorf("done thread = %d, want 101", out.ThreadID)
	}
	if !strings.Contains(out.Text, "🏆 任务已完成") || !strings.Contains(out.Text, "All 5 tests passed") {
		t.Errorf("done text: %q", out.Text)
	}
}

func TestQuietModeAtDeskIsSilent(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// At desk: idle for only 10 seconds (< 3 minutes)
	d.SetIdleChecker(fakeIdleChecker{idle: 10 * time.Second})

	d.handleStatus(context.Background(), domain.Event{
		Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusDone,
	})
	sent := ft.sentLog()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	if sent[0].Notify {
		t.Errorf("at desk must be silent (Notify: false), got Notify=true")
	}

	// Blocked question at desk should also be silent
	fh.screen = domain.Screen{Text: "confirm?"}
	fh.dialog = piCursorDialog()
	d.handleStatus(context.Background(), domain.Event{
		Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusBlocked,
	})
	sent = ft.sentLog()
	if len(sent) != 2 {
		t.Fatalf("sent %d messages", len(sent))
	}
	if sent[1].Notify {
		t.Errorf("blocked at desk must be silent (Notify: false), got Notify=true")
	}
}

func TestQuietModeAwayRings(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Away from desk: idle for 10 minutes (>= 3 minutes)
	d.SetIdleChecker(fakeIdleChecker{idle: 10 * time.Minute})

	fh.screen = domain.Screen{Text: "confirm?"}
	fh.dialog = piCursorDialog()
	d.handleStatus(context.Background(), domain.Event{
		Kind: domain.EventAgentStatusChanged, PaneID: "w1:p1", Status: domain.StatusBlocked,
	})
	sent := ft.sentLog()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	if !sent[0].Notify {
		t.Errorf("away must ring (Notify: true), got Notify=false")
	}
}

func TestAgentDetectedReopensClosedTopic(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture()[:1], domain.Dialog{}, "")
	d.mapping.Link("w1:p1", &Entry{ThreadID: 101, Name: "π - task", Status: domain.StatusWorking, Closed: true})

	d.handleEvent(context.Background(), domain.Event{Kind: domain.EventAgentDetected, PaneID: "w1:p1"})
	if len(ft.reopened) != 1 || ft.reopened[0] != 101 {
		t.Fatalf("reopened: %v", ft.reopened)
	}
	if got := len(fh.subPanes); got > 0 {
		// handleEvent does not rebuild the subscription itself; Run does.
		t.Fatalf("unexpected subscribe: %v", fh.subPanes)
	}
	if e := d.mapping.Topics["w1:p1"]; e.Closed {
		t.Fatal("entry still closed after reopen")
	}
}

func TestAnswerInTopicPrompts(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	d.answerInTopic(context.Background(), domain.TopicMessage{ThreadID: 101, FromID: 7, Text: "run the tests"})
	if got := fh.promptLog(); len(got) != 1 || got[0].pane != "w1:p1" || got[0].text != "run the tests" {
		t.Fatalf("prompts: %v", got)
	}
	if len(ft.sentLog()) != 0 {
		t.Fatalf("a successful prompt needs no reply: %v", ft.sentLog())
	}

	// unknown thread is ignored
	d.answerInTopic(context.Background(), domain.TopicMessage{ThreadID: 999, FromID: 7, Text: "where?"})
	if got := len(fh.promptLog()); got != 1 {
		t.Fatalf("unknown thread prompted anyway: %v", fh.promptLog())
	}

	// slash commands are not prompts
	d.answerInTopic(context.Background(), domain.TopicMessage{ThreadID: 101, FromID: 7, Text: "/help"})
	if got := len(fh.promptLog()); got != 1 {
		t.Fatalf("slash command prompted anyway: %v", fh.promptLog())
	}

	// attachments get the not-yet reply
	d.answerInTopic(context.Background(), domain.TopicMessage{
		ThreadID: 101, FromID: 7, Caption: "look",
		Attachment: &domain.Attachment{Kind: domain.AttachmentPhoto, FileID: "f"},
	})
	sent := ft.sentLog()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "not supported") {
		t.Fatalf("attachment reply: %+v", sent)
	}
}

func TestAnswerInTopicReportsPromptFailure(t *testing.T) {
	d, fh, ft := newTestDaemon(t, agentsFixture(), domain.Dialog{}, "")
	fh.promptErr = context.DeadlineExceeded
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	d.answerInTopic(context.Background(), domain.TopicMessage{ThreadID: 101, FromID: 7, Text: "go"})
	sent := ft.sentLog()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "⚠️") {
		t.Fatalf("failure reply: %+v", sent)
	}
}

func TestSweepPicksUpNewAgent(t *testing.T) {
	// Herdr 0.9.1 has no workspace-wide pane events, so the periodic sweep
	// is what notices a pane born after the last reconcile.
	d, fh, ft := newTestDaemon(t, agentsFixture()[:1], piCursorDialog(), "screen")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	fh.agents = append(fh.agents, domain.Agent{
		PaneID: "w1:p7", Kind: domain.KindPi, DisplayAgent: "late arrival", Status: domain.StatusBlocked,
	})
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(ft.created); got != 2 {
		t.Fatalf("sweep created %d topics, want 2", got)
	}
	if ft.created[1].name != "late arrival" {
		t.Fatalf("second topic: %+v", ft.created[1])
	}
	// The blocked newcomer gets its question on the same sweep.
	sent := ft.sentLog()
	if len(sent) != 1 || sent[0].ThreadID != 102 {
		t.Fatalf("blocked newcomer question: %+v", sent)
	}
}
