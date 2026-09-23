package domain

import (
	"regexp"
	"strings"
)

var codexAnswerHint = regexp.MustCompile(`(?i)^shift\s*\+\s*←\s+to answer$`)
var codexQuestionCount = regexp.MustCompile(`^\?\s+\d+\s+questions?(?:\s+·\s+.+)?$`)

const codexQueueHeading = "• Queued follow-up inputs"

// CodexQuestionPending requires the live, collapsed question panel at the
// bottom of the screen. A mention in transcript text must not send keys.
func CodexQuestionPending(screen string) bool {
	return codexPendingBody(screen) != ""
}

func codexPendingBody(screen string) string {
	raw := screenLines(strings.TrimSpace(screen))
	if len(raw) > 0 && shellPromptLine.MatchString(raw[len(raw)-1]) {
		return ""
	}
	lines := screenLines(TrimTrailingPrompts(screen))
	var tail []string
	for _, line := range lines {
		if s := strings.TrimSpace(line); s != "" {
			tail = append(tail, s)
		}
	}
	n := len(tail)
	if n < 3 || tail[n-3] != codexQueueHeading ||
		!codexQuestionCount.MatchString(tail[n-2]) || !codexAnswerHint.MatchString(tail[n-1]) {
		return ""
	}
	// Keep the original wording, but omit the changing age counter.
	count, _, _ := strings.Cut(tail[n-2], " · ")
	return strings.Join([]string{tail[n-3], count, tail[n-1]}, "\n")
}

// parseCodexQuestion recognizes the expanded async-question panel measured
// on the live Codex terminal. The footer anchors it to the active composer.
func parseCodexQuestion(screen string) (Dialog, bool) {
	lines := screenLines(strings.TrimSpace(screen))
	if len(lines) == 0 {
		return Dialog{}, false
	}
	footer := strings.ToLower(strings.TrimSpace(lines[len(lines)-1]))
	if !strings.Contains(footer, "enter submit") || !strings.Contains(footer, "skip") || !strings.Contains(footer, "main prompt") {
		return Dialog{}, false
	}
	start := -1
	for i := len(lines) - 2; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == codexQueueHeading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return Dialog{}, false
	}
	bodyLines := lines[start : len(lines)-1]
	for len(bodyLines) > 0 {
		last := strings.TrimSpace(bodyLines[len(bodyLines)-1])
		if last != "" && last != "Type your answer" {
			break
		}
		bodyLines = bodyLines[:len(bodyLines)-1]
	}
	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	if body == "" {
		return Dialog{}, false
	}
	// Numbered options in this panel are navigated, not digit shortcuts.
	if d, ok := parseNumbered(bodyLines, codexProfile); ok {
		d.Style = StyleCursor
		d.Body, d.TextInput = body, true
		return d, true
	}
	return Dialog{Kind: KindCodex, Style: StyleText, Title: body, Body: body, TextInput: true}, true
}
