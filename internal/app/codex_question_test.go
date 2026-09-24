package app

import (
	"context"
	"strings"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func TestCodexQuestionPostsOnlyPanelAndAcceptsText(t *testing.T) {
	const screen = `• Ran go test ./...

• Queued follow-up inputs

  请说明输入框的问题。

  Type your answer

  enter submit   ctrl + ] skip   ⌥ + ↓ main prompt
`
	agents := agentsFixture()[:1]
	agents[0].Kind, agents[0].Status = domain.KindCodex, domain.StatusBlocked
	d, fh, ft := newTestDaemon(t, agents, domain.ParseDialog(screen, domain.KindCodex), screen)
	ctx := context.Background()
	if err := d.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	sent := ft.sentLog()
	if len(sent) != 1 || sent[0].Text != "❓ 请说明输入框的问题。\n\n请直接在此话题发送文字回答。" {
		t.Fatalf("question text: %+v", sent)
	}
	fh.screen.Text = "• More unrelated activity\n" + screen
	d.PostBlocked(ctx, agents[0])
	if len(ft.sentLog()) != 1 {
		t.Fatal("terminal activity duplicated the same question")
	}
	fh.promptErr = context.DeadlineExceeded // agent.prompt must never be called.
	d.answerInTopic(ctx, domain.TopicMessage{ThreadID: 101, Text: "输入框混进消息了"})
	if len(fh.typed) != 1 || fh.typed[0].text != "输入框混进消息了" {
		t.Fatalf("text answer not delivered to composer: %v", fh.typed)
	}
	if len(ft.sentLog()) != 1 {
		t.Fatalf("unexpected error reply: %+v", ft.sentLog())
	}
}

func TestCodexDirectQuestionAcceptsTopicAnswer(t *testing.T) {
	const screen = `• Ran a tool

› > 请说明输入框的问题。

  Type your answer

  enter submit   ctrl+] skip   ⌥+↓ main prompt
`
	agents := agentsFixture()[:1]
	agents[0].Kind, agents[0].Status = domain.KindCodex, domain.StatusBlocked
	d, fh, ft := newTestDaemon(t, agents, domain.ParseDialog(screen, domain.KindCodex), screen)
	ctx := context.Background()
	if err := d.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if sent := ft.sentLog(); len(sent) != 1 || sent[0].Text != "❓ 请说明输入框的问题。\n\n请直接在此话题发送文字回答。" {
		t.Fatalf("direct question post: %+v", sent)
	}
	fh.promptErr = context.DeadlineExceeded
	d.answerInTopic(ctx, domain.TopicMessage{ThreadID: 101, Text: "答案"})
	if len(fh.typed) != 1 || fh.typed[0].text != "答案" {
		t.Fatalf("answer did not use pane input: %+v", fh.typed)
	}
}

func TestTextEntryRetryPreservesPendingAnswer(t *testing.T) {
	dg := domain.Dialog{Kind: domain.KindClaude, Style: domain.StyleNumbered, Title: "What next?",
		Choices:   []domain.Choice{{Number: 1, Label: "retry", Key: "1"}, {Number: 2, Label: "abort", Key: "2"}},
		TextEntry: 3, TextLabel: "Type something"}
	d, fh, ft := blockedDaemon(t, dg)
	d.handleButton(context.Background(), press("t:3", 1))
	fh.typeErr = context.DeadlineExceeded
	d.answerInTopic(context.Background(), domain.TopicMessage{ThreadID: 101, Text: "my answer"})
	if _, ok := d.typing["w1:p1"]; !ok {
		t.Fatal("failed answer discarded typing wait")
	}
	if sent := ft.sentLog(); len(sent) != 2 || !strings.Contains(sent[1].Text, "⚠️") {
		t.Fatalf("missing failure reply: %+v", sent)
	}
	fh.typeErr = nil
	d.answerInTopic(context.Background(), domain.TopicMessage{ThreadID: 101, Text: "my answer"})
	if len(fh.typed) != 1 {
		t.Fatalf("retry not delivered: %v", fh.typed)
	}
}

func TestClickShiftLeftOpensCodexQuestionCard(t *testing.T) {
	const queuedScreen = `• Queued follow-up inputs
  ? 1 question
    shift + ← to answer
› Ask Codex to do anything
`
	const expandedScreen = `• Queued follow-up inputs

  请问要继续执行吗？

  Type your answer

  enter submit   ctrl + ] skip   ⌥ + ↓ main prompt
`
	agents := agentsFixture()[:1]
	agents[0].Kind, agents[0].Status = domain.KindCodex, domain.StatusBlocked
	d, fh, ft := newTestDaemon(t, agents, domain.ParseDialog(queuedScreen, domain.KindCodex), queuedScreen)
	ctx := context.Background()
	if err := d.reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	sent := ft.sentLog()
	if len(sent) != 1 {
		t.Fatalf("expected 1 initial message, got %d", len(sent))
	}
	if len(sent[0].Buttons) != 1 || sent[0].Buttons[0].Text != "Shift + ←" || sent[0].Buttons[0].Data != domain.CallbackOpenQuestion {
		t.Fatalf("unexpected button: %+v", sent[0].Buttons)
	}

	expandedDialog := domain.ParseDialog(expandedScreen, domain.KindCodex)
	fh.dialogAfterKeys = &expandedDialog
	fh.screen.Text = expandedScreen

	d.handleButton(ctx, press(domain.CallbackOpenQuestion, 1))

	if len(fh.keys) != 1 || len(fh.keys[0].keys) != 1 || fh.keys[0].keys[0] != "shift+left" {
		t.Fatalf("expected shift+left key, got: %+v", fh.keys)
	}
	if len(ft.sentLog()) != 1 {
		t.Fatalf("should not send new message, sent count=%d", len(ft.sentLog()))
	}
	var questionEdited bool
	for _, e := range ft.editedText {
		if e.messageID == 1 && strings.Contains(e.text, "请问要继续执行吗？") {
			questionEdited = true
			break
		}
	}
	if !questionEdited {
		t.Fatalf("expected question card (messageID 1) to be edited, got: %+v", ft.editedText)
	}
}
