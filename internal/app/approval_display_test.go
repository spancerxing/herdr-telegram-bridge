package app

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// Synthetic fixtures verify rendering and callback mapping, not real CLI behavior.
func TestApprovalDisplayAndCallback(t *testing.T) {
	for _, tc := range []struct {
		kind     domain.Kind
		screen   string
		captions []string
		keys     []string
	}{
		{domain.KindCodex, "Run command?\n\n$ npm test\n\n› 1. Yes (y)\n  2. Deny (esc)\n\nPress enter to confirm or esc to cancel", []string{"1. Yes (y)", "2. Deny (esc)"}, []string{"down", "enter"}},
		{domain.KindClaude, "Allow tool?\n\n❯ 1) Yes\n  2) Deny\nEnter to confirm, esc to cancel", []string{"1) Yes", "2) Deny"}, []string{"2"}},
		{domain.KindAgy, "requesting permission for: run_command\n\nDo you want to proceed?\n  1. Yes\n  2. No\n\ntab amend  esc to cancel", []string{"1. Yes", "2. No"}, []string{"2"}},
		{domain.KindPi, "Herdr bridge self-test\nContinue with the bridge test?\n\n→ Yes\n  No\n\n↑↓ navigate  enter select  escape/ctrl+c cancel", []string{"Yes", "No"}, []string{"down", "enter"}},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			dg := domain.ParseDialog(tc.screen, tc.kind)
			buttons := choiceButtons(dg)
			if len(buttons) != len(tc.captions) {
				t.Fatalf("buttons = %+v", buttons)
			}
			for i, b := range buttons {
				if b.Text != tc.captions[i] || b.Data != strconv.Itoa(i+1) {
					t.Fatalf("button = %+v", b)
				}
			}
			text := questionText(dg, tc.screen)
			firstLine := strings.Split(tc.screen, "\n")[0]
			if strings.Count(text, firstLine) != 1 {
				t.Fatalf("duplicated question: %s", text)
			}
			if strings.Contains(text, "esc to cancel") || strings.Contains(text, "↑↓ navigate") {
				t.Fatalf("footer leaked: %s", text)
			}
			d, fh, _ := blockedDaemon(t, dg)
			d.handleButton(context.Background(), press("2", 1))
			if got := fh.keyLog(); len(got) != 1 || !reflect.DeepEqual(got[0].keys, tc.keys) {
				t.Fatalf("callback keys = %+v", got)
			}
			// Ended test panes must not turn scrollback into live approvals.
			if stale := domain.ParseDialog(tc.screen+"\nuser@host project %\n", tc.kind); stale.Usable() {
				t.Fatalf("stale dialog = %+v", stale)
			}
		})
	}
}
