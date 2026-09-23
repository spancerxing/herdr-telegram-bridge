package app

import (
	"context"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// blockedDaemon returns a daemon whose pi agent is blocked with the cursor
// dialog posted as message 1 in thread 101.
func blockedDaemon(t *testing.T, dg domain.Dialog) (*Daemon, *fakeHerdr, *fakeTelegram) {
	t.Helper()
	agents := agentsFixture()
	agents[0].Status = domain.StatusBlocked
	d, fh, ft := newTestDaemon(t, agents, dg, "screen body")
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(ft.sentLog()) != 1 {
		t.Fatalf("question not posted: %+v", ft.sentLog())
	}
	return d, fh, ft
}

func press(data string, messageID int) domain.ButtonPress {
	return domain.ButtonPress{ChatID: -100, ThreadID: 101, MessageID: messageID, FromID: 7, CallbackID: "cb", Data: data}
}

func TestButtonAnswersCursorDialogWithKeys(t *testing.T) {
	d, fh, ft := blockedDaemon(t, piCursorDialog())

	d.handleButton(context.Background(), press("1", 1))
	keys := fh.keyLog()
	if len(keys) != 1 || keys[0].pane != "w1:p1" {
		t.Fatalf("keys: %v", keys)
	}
	if got := keys[0].keys; len(got) != 1 || got[0] != domain.KeyEnter {
		t.Fatalf("Yes must be answered with enter (cursor starts on it): %v", got)
	}
	answers := ft.answerLog()
	if len(answers) != 1 || answers[0].text != "sent: Yes" {
		t.Fatalf("answers: %+v", answers)
	}
	if kb := ft.keyboardLog(); len(kb) != 1 || len(kb[0].buttons) != 0 {
		t.Fatalf("keyboard not retired: %+v", kb)
	}
	if _, open := d.questions["w1:p1"]; open {
		t.Fatal("question still open")
	}
}

func TestButtonOnRetiredMessageIsExpired(t *testing.T) {
	d, _, ft := blockedDaemon(t, piCursorDialog())

	// After a restart nothing knows message 1; the press must not send keys.
	d.handleButton(context.Background(), press("1", 999))
	if got := len(ft.keyboardLog()); got != 1 {
		t.Fatalf("stale press should still clear the keyboard: %+v", ft.keyboardLog())
	}
	answers := ft.answerLog()
	if len(answers) != 1 || answers[0].text != "expired" {
		t.Fatalf("answers: %+v", answers)
	}
}

func TestButtonMultiTogglesThenSubmits(t *testing.T) {
	dg := domain.Dialog{
		Kind:  domain.KindClaude,
		Style: domain.StyleNumbered,
		Title: "Select tools",
		Choices: []domain.Choice{
			{Number: 1, Label: "go", Key: "1"},
			{Number: 2, Label: "rust", Key: "2"},
		},
		Multi: true,
	}
	d, fh, ft := blockedDaemon(t, dg)

	d.handleButton(context.Background(), press("1", 1))
	if keys := fh.keyLog(); len(keys) != 1 || keys[0].keys[0] != "1" {
		t.Fatalf("toggle keys: %v", fh.keyLog())
	}
	// The dialog is still up: the keyboard is redrawn, not retired.
	kb := ft.keyboardLog()
	if len(kb) != 1 || len(kb[0].buttons) != 3 { // two options + Submit
		t.Fatalf("keyboard not redrawn after toggle: %+v", kb)
	}
	if answers := ft.answerLog(); len(answers) != 1 || answers[0].text != "toggled" {
		t.Fatalf("answers: %+v", answers)
	}

	d.handleButton(context.Background(), press(domain.CallbackSubmit, 1))
	if keys := fh.keyLog(); len(keys) != 2 {
		t.Fatalf("submit keys: %v", keys)
	}
	kb = ft.keyboardLog()
	if len(kb) != 2 || len(kb[1].buttons) != 0 {
		t.Fatalf("keyboard not retired on submit: %+v", kb)
	}
	if answers := ft.answerLog(); len(answers) != 2 || answers[1].text != "submitted" {
		t.Fatalf("answers: %+v", answers)
	}
}

func TestButtonTextEntryWaitsForMessage(t *testing.T) {
	dg := domain.Dialog{
		Kind:  domain.KindClaude,
		Style: domain.StyleNumbered,
		Title: "What next?",
		Choices: []domain.Choice{
			{Number: 1, Label: "retry", Key: "1"},
			{Number: 2, Label: "abort", Key: "2"},
		},
		TextEntry: 3,
		TextLabel: "Type something",
	}
	d, fh, ft := blockedDaemon(t, dg)
	if buttons := ft.sentLog()[0].Buttons; len(buttons) != 3 || buttons[2].Data != "t:3" {
		t.Fatalf("text-entry button missing: %+v", buttons)
	}

	d.handleButton(context.Background(), press("t:3", 1))
	if keys := fh.keyLog(); len(keys) != 1 || keys[0].keys[0] != "3" {
		t.Fatalf("entry keys: %v", fh.keyLog())
	}
	if _, waiting := d.typing["w1:p1"]; !waiting {
		t.Fatal("typing wait not armed")
	}
	if kb := ft.keyboardLog(); len(kb) != 1 || len(kb[0].buttons) != 0 {
		t.Fatalf("keyboard not retired for typing: %+v", kb)
	}

	// The next message in the topic is the free text.
	d.answerInTopic(context.Background(), domain.TopicMessage{ThreadID: 101, FromID: 7, Text: "do the thing"})
	if len(fh.typed) != 1 || fh.typed[0].text != "do the thing" || len(fh.promptLog()) != 0 {
		t.Fatalf("text answer must bypass agent.prompt: typed=%v prompts=%v", fh.typed, fh.promptLog())
	}
	if _, waiting := d.typing["w1:p1"]; waiting {
		t.Fatal("typing wait not consumed")
	}
}

func TestButtonOnAnsweredDialogIsDeclined(t *testing.T) {
	d, fh, ft := blockedDaemon(t, piCursorDialog())

	// The pane already left blocked and the re-read finds no dialog.
	d.agents["w1:p1"] = domain.Agent{PaneID: "w1:p1", Kind: domain.KindPi, Status: domain.StatusWorking}
	fh.dialog = domain.Dialog{}

	d.handleButton(context.Background(), press("1", 1))
	if keys := fh.keyLog(); keys != nil {
		t.Fatalf("keys sent to an answered dialog: %v", keys)
	}
	answers := ft.answerLog()
	if len(answers) != 1 || answers[0].text != "the agent moved on" {
		t.Fatalf("answers: %+v", answers)
	}
}

func TestPostBlockedDedupsSameScreen(t *testing.T) {
	d, fh, ft := blockedDaemon(t, piCursorDialog())

	// A duplicate blocked event with the same screen stays quiet.
	d.PostBlocked(context.Background(), d.agents["w1:p1"])
	if got := len(ft.sentLog()); got != 1 {
		t.Fatalf("same screen posted %d times", got)
	}

	// A new question supersedes and retires the first.
	fh.screen = domain.Screen{Text: "a different question"}
	d.PostBlocked(context.Background(), d.agents["w1:p1"])
	sent := ft.sentLog()
	if len(sent) != 2 {
		t.Fatalf("new screen should post again: %d", len(sent))
	}
	kb := ft.keyboardLog()
	if len(kb) != 1 || len(kb[0].buttons) != 0 {
		t.Fatalf("superseded keyboard not retired: %+v", kb)
	}
}

func TestSameQuestionReturnsInNextBlockedEpisode(t *testing.T) {
	for _, viaSweep := range []bool{false, true} {
		name := "events"
		if viaSweep {
			name = "sweep"
		}
		t.Run(name, func(t *testing.T) {
			d, fh, ft := blockedDaemon(t, piCursorDialog())
			ctx := context.Background()
			d.handleButton(ctx, press("1", 1))
			// Until the agent moves on, duplicate observations must not
			// resurrect the card whose button was just pressed.
			d.PostBlocked(ctx, d.agents["w1:p1"])
			if got := len(ft.sentLog()); got != 1 {
				t.Fatalf("answered card reposted before status changed: %d", got)
			}
			for _, status := range []domain.Status{domain.StatusWorking, domain.StatusBlocked} {
				if viaSweep {
					fh.agents[0].Status = status
					if err := d.reconcile(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					d.handleStatus(ctx, domain.Event{PaneID: "w1:p1", Status: status})
				}
			}
			if got := len(ft.sentLog()); got != 2 {
				t.Fatalf("next identical question was lost: sent %d, want 2", got)
			}
			// The old card stays expired; the new card can answer again.
			d.handleButton(ctx, press("1", 1))
			if got := len(fh.keyLog()); got != 1 {
				t.Fatalf("old card sent keys again: %d", got)
			}
			d.handleButton(ctx, press("2", 2))
			if got := len(fh.keyLog()); got != 2 {
				t.Fatalf("new card cannot be answered: %d", got)
			}
		})
	}
}

func TestSweepRetiresQuestionWhenAgentMovesOn(t *testing.T) {
	d, fh, ft := blockedDaemon(t, piCursorDialog())
	fh.agents[0].Status = domain.StatusWorking
	if err := d.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, open := d.questions["w1:p1"]; open {
		t.Fatal("question survived the observed end of its blocked episode")
	}
	if kb := ft.keyboardLog(); len(kb) != 1 || len(kb[0].buttons) != 0 {
		t.Fatalf("old keyboard not retired: %+v", kb)
	}
}
