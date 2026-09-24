package domain

import (
	"strings"
	"testing"
)

const codexAnimatedComposer = `
⠁             ⠄          ⢀        ⢀                ⠐     ⠐⠂
›⠁Ask Codex to do anything⡀  ⠈       ⠂              ⠐ ⠁
      ⠠⢀                  ⠄         ⠠        ⢀ ⢀
  gpt-6-astra high · ~/task/project · Context 51% used
`

func TestCodexPendingQuestionAndAnimatedComposer(t *testing.T) {
	const history = "• Ran go test ./...\n• 任务已经完成。"
	const pending = "\n\n• Queued follow-up inputs\n  ? 1 question · 16s\n    shift + ← to answer\n"
	screen := history + pending + codexAnimatedComposer
	if !CodexQuestionPending(screen) {
		t.Fatal("live pending question not recognized")
	}
	if got := TrimScreenChrome(screen); got != history {
		t.Fatalf("composer or pending hints leaked: %q", got)
	}
	if got := TrimScreenChrome("结果包含盲文 ⠁⠃\n" + codexAnimatedComposer); got != "结果包含盲文 ⠁⠃" {
		t.Fatalf("body modified while cleaning composer: %q", got)
	}
	for _, stale := range []string{
		screen + "\nuser@host project %",
		history + pending + "\n• Another tool finished.\n" + codexAnimatedComposer,
		"The shortcut is shift + ← to answer.\n" + codexAnimatedComposer,
	} {
		if CodexQuestionPending(stale) {
			t.Fatalf("history mistaken for active question: %q", stale)
		}
	}
}

func TestParseCodexExpandedTextQuestion(t *testing.T) {
	const screen = `• Ran go test ./...
  unrelated terminal history

• Queued follow-up inputs

  输入框的问题是消息混入输入框，还是答案没有发送成功？

  Type your answer

  enter submit   ctrl + ] skip   ⌥ + ↓ main prompt
`
	d := ParseDialog(screen, KindCodex)
	if !d.Usable() || d.Style != StyleText || !d.TextInput || d.Title != "输入框的问题是消息混入输入框，还是答案没有发送成功？" {
		t.Fatalf("expanded text question: %+v", d)
	}
	if strings.Contains(d.Body, "history") || strings.Contains(d.Body, "Type your answer") {
		t.Fatalf("question includes chrome: %q", d.Body)
	}
	if d := ParseDialog(screen+"\nuser@host project %", KindCodex); d.Usable() {
		t.Fatalf("stale question revived: %+v", d)
	}
}

func TestParseCodexDirectTextQuestion(t *testing.T) {
	const screen = `• Ran a tool
  └ unrelated history

› > 请说明输入框的问题。它可以换行吗？
    请提供一个具体例子。

  Type your answer

  enter submit   ctrl+] skip   ⌥+↓ main prompt
`
	d := ParseDialog(screen, KindCodex)
	if !d.Usable() || d.Style != StyleText || !d.TextInput || d.Title != "请说明输入框的问题。它可以换行吗？\n    请提供一个具体例子。" {
		t.Fatalf("direct text question: %+v", d)
	}
	if strings.Contains(d.Body, "unrelated history") || strings.Contains(d.Body, "Type your answer") {
		t.Fatalf("question includes transcript or chrome: %q", d.Body)
	}
	if stale := ParseDialog(screen+"\n› Ask Codex to do anything", KindCodex); stale.Usable() {
		t.Fatalf("historical question revived: %+v", stale)
	}
}

func TestParseCodexExpandedChoiceQuestion(t *testing.T) {
	const screen = `• Queued follow-up inputs

  选择处理方式

› 1. 更新版本
  2. 保留现状

  Type your answer

  enter submit   ctrl + ] skip   ⌥ + ↓ main prompt
`
	d := ParseDialog(screen, KindCodex)
	if !d.Usable() || d.Style != StyleCursor || !d.TextInput || len(d.Choices) != 2 {
		t.Fatalf("expanded choices: %+v", d)
	}
	if keys := d.KeysFor(d.Choices[1]); strings.Join(keys, ",") != "down,enter" {
		t.Fatalf("choice should navigate, not type its digit: %v", keys)
	}
}
