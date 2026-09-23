package domain

import (
	"reflect"
	"strings"
	"testing"
)

// The screens below are reconstructions of what each kind's Herdr detection
// manifest says the kind draws (agent-detection/remote/*.toml). Each test
// names the manifest rule it comes from so a manifest bump can be traced
// back to the case that has to change with it.

// claude.toml rule generic_permission_prompt: "do you want to proceed?" with
// numbered options prefixed by the ❯ cursor.
const claudePermission = ` Bash command

   rm -rf /tmp/scratch
   Clean the scratch directory

 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, and don't ask again for: rm *
   3. No
 Esc to cancel · Tab to amend
`

// claude.toml: a multi-select dialog. Every option is boxed and the dialog
// ends with a bare Submit row; "Type something." is the free-text service
// entry and must not become a button.
const claudeMultiSelect = ` Which files should change?
 ❯ 1. [ ] internal/a.go
   2. [x] internal/b.go
   3. [ ] internal/c.go
   4. [ ] Type something.
      Submit
`

// codex.toml rule trust_directory: the prompt opens with "> You are in",
// and codex marks the selected row with › rather than ❯.
const codexTrustDirectory = `> You are in /Users/example/project
Do you trust the contents of this directory?

› 1. Yes, proceed
  2. No, quit
`

// codex.toml rule weak_blocker: the unnumbered [y/n] shape.
const codexYesNo = `Allow command?

> npm install left-pad

[y/n]
`

// agy.toml rule permission_prompt: the only blocked rule agy ships.
const agyPermission = `requesting permission for: run_command

Do you want to proceed?
  1. Yes
  2. No
`

// pi ships no blocked rule at all (pi.toml version 2026.09.14.1 only has
// working_literal and working_border), so pi's dialog body is unmeasured and
// has to survive the generic path.
const piGeneric = `You have unsaved changes.

  1. Save and continue
  2. Discard
  3. Cancel
`

func TestParseDialogNumbered(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		kind   Kind
		want   []Choice
	}{
		{
			name:   "claude permission prompt",
			screen: claudePermission,
			kind:   KindClaude,
			want: []Choice{
				{Number: 1, Label: "Yes", Caption: "1. Yes", Key: "1"},
				{Number: 2, Label: "Yes, and don't ask again for: rm *", Caption: "2. Yes, and don't ask again for: rm *", Key: "2"},
				{Number: 3, Label: "No", Caption: "3. No", Key: "3"},
			},
		},
		{
			name:   "codex trust directory with › cursor",
			screen: codexTrustDirectory,
			kind:   KindCodex,
			want: []Choice{
				{Number: 1, Label: "Yes, proceed", Caption: "1. Yes, proceed", Key: "1"},
				{Number: 2, Label: "No, quit", Caption: "2. No, quit", Key: "2"},
			},
		},
		{
			name:   "agy permission prompt",
			screen: agyPermission,
			kind:   KindAgy,
			want: []Choice{
				{Number: 1, Label: "Yes", Caption: "1. Yes", Key: "1"},
				{Number: 2, Label: "No", Caption: "2. No", Key: "2"},
			},
		},
		{
			name:   "pi falls back to the generic profile",
			screen: piGeneric,
			kind:   KindPi,
			want: []Choice{
				{Number: 1, Label: "Save and continue", Caption: "1. Save and continue", Key: "1"},
				{Number: 2, Label: "Discard", Caption: "2. Discard", Key: "2"},
				{Number: 3, Label: "Cancel", Caption: "3. Cancel", Key: "3"},
			},
		},
		{
			name:   "unknown kind still parses a plain numbered dialog",
			screen: piGeneric,
			kind:   "",
			want: []Choice{
				{Number: 1, Label: "Save and continue", Caption: "1. Save and continue", Key: "1"},
				{Number: 2, Label: "Discard", Caption: "2. Discard", Key: "2"},
				{Number: 3, Label: "Cancel", Caption: "3. Cancel", Key: "3"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := ParseDialog(tc.screen, tc.kind)
			if d.Style != StyleNumbered {
				t.Fatalf("style = %q, want numbered", d.Style)
			}
			if !reflect.DeepEqual(d.Choices, tc.want) {
				t.Errorf("choices = %+v, want %+v", d.Choices, tc.want)
			}
			if d.Multi {
				t.Errorf("Multi = true, want false")
			}
			if d.Title != expectedTitle(tc.screen, tc.kind) {
				t.Errorf("Title = %q, want %q", d.Title, expectedTitle(tc.screen, tc.kind))
			}
		})
	}
}

func expectedTitle(screen string, kind Kind) string {
	switch screen {
	case claudePermission:
		return "Do you want to proceed?"
	case codexTrustDirectory:
		return "> You are in /Users/example/project · Do you trust the contents of this directory?"
	case agyPermission:
		return "Do you want to proceed?"
	case piGeneric:
		return "You have unsaved changes."
	default:
		return ""
	}
}

func TestParseDialogMultiSelect(t *testing.T) {
	d := ParseDialog(claudeMultiSelect, KindClaude)
	if d.Style != StyleNumbered {
		t.Fatalf("style = %q, want numbered", d.Style)
	}
	if !d.Multi {
		t.Fatalf("Multi = false, want true")
	}
	// The Type something row is the free-text entry, not a button.
	want := []Choice{
		{Number: 1, Label: "internal/a.go", Caption: "1. [ ] internal/a.go", Key: "1"},
		{Number: 2, Label: "internal/b.go", Caption: "2. [x] internal/b.go", Key: "2"},
		{Number: 3, Label: "internal/c.go", Caption: "3. [ ] internal/c.go", Key: "3"},
	}
	if !reflect.DeepEqual(d.Choices, want) {
		t.Errorf("choices = %+v, want %+v", d.Choices, want)
	}
	if d.TextEntry != 4 {
		t.Errorf("TextEntry = %d, want 4", d.TextEntry)
	}
	if d.TextLabel != "Type something" {
		t.Errorf("TextLabel = %q, want %q", d.TextLabel, "Type something")
	}
	// The cursor rests on row 1 and Submit is row 5, so submitting walks
	// down four rows and confirms.
	if got, want := d.SubmitKeys(), []string{"down", "down", "down", "down", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SubmitKeys = %v, want %v", got, want)
	}
}

// The yes/no marker must end a line: that is what separates a prompt from an
// agent explaining what "[y/n]" means.
func TestParseDialogYesNoNeedsLineEnd(t *testing.T) {
	const inline = `Allow command?
Run [y/n] to answer
`
	if d := ParseDialog(inline, KindCodex); d.Usable() {
		t.Errorf("parsed a dialog from a marker mid-line: %+v", d)
	}
	const trailing = `Allow command?
Continue? [y/n]
`
	d := ParseDialog(trailing, KindCodex)
	if d.Style != StyleYesNo {
		t.Errorf("style = %q, want yesno for a trailing marker", d.Style)
	}
}

func TestParseDialogYesNo(t *testing.T) {
	d := ParseDialog(codexYesNo, KindCodex)
	if d.Style != StyleYesNo {
		t.Fatalf("style = %q, want yesno", d.Style)
	}
	want := []Choice{
		{Label: "Yes", Key: "y"},
		{Label: "No", Key: "n"},
	}
	if !reflect.DeepEqual(d.Choices, want) {
		t.Errorf("choices = %+v, want %+v", d.Choices, want)
	}
	if d.Multi {
		t.Error("Multi = true, want false")
	}
	if d.Title == "" {
		t.Error("Title is empty")
	}
}

// Synthetic cursor-only fixture; not a captured Shift+Left interaction.
// It tests arrow navigation independently of numbered approval dialogs.
func TestParseDialogCodexCursorApproval(t *testing.T) {
	const screen = `Would you like to run this command?

  $ npm test

  Yes
› Yes, and don't ask again for commands that start with npm
  No

Press enter to confirm or esc to cancel
`
	d := ParseDialog(screen, KindCodex)
	if d.Style != StyleCursor {
		t.Fatalf("style = %q, want cursor; choices=%+v", d.Style, d.Choices)
	}
	want := []Choice{
		{Number: 1, Label: "Yes", Key: "1"},
		{Number: 2, Label: "Yes, and don't ask again for commands that start with npm", Key: "2"},
		{Number: 3, Label: "No", Key: "3"},
	}
	if !reflect.DeepEqual(d.Choices, want) {
		t.Errorf("choices = %+v, want %+v", d.Choices, want)
	}
	if d.Cursor != 2 {
		t.Fatalf("Cursor = %d, want 2", d.Cursor)
	}
	if got, want := d.KeysFor(d.Choices[0]), []string{"up", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(Yes) = %v, want %v", got, want)
	}
	if got, want := d.KeysFor(d.Choices[2]), []string{"down", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(No) = %v, want %v", got, want)
	}
}

// The parser must stay quiet when the numbers are prose rather than a dialog.
// Buttons on a transcript list would send digits the agent never asked for.
func TestParseDialogRejectsNonDialogs(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		kind   Kind
	}{
		{
			name: "numbered prose with text after it",
			screen: `Here are the steps:

1. Install Go
2. Build the project

Then run the tests.
`,
			kind: KindClaude,
		},
		{
			name: "a single numbered line is a list fragment",
			screen: `  1. Continue
`,
			kind: KindClaude,
		},
		{
			name: "numbering with a gap",
			screen: `  1. Yes
  3. No
`,
			kind: KindClaude,
		},
		{
			name: "ten options cannot be answered with a single key",
			screen: `  1. a
  2. b
  3. c
  4. d
  5. e
  6. f
  7. g
  8. h
  9. i
 10. j
`,
			kind: KindClaude,
		},
		{
			name:   "empty screen",
			screen: "",
			kind:   KindClaude,
		},
		{
			name: "yes/no markers on a kind that does not draw them",
			screen: `Shall I continue? [y/n]
`,
			kind: KindClaude,
		},
		{
			// Regression: this plugin's own source, displayed in a pane, made
			// a suffix test grow Yes/No buttons on a working pi agent.
			name:   "a parenthesized marker quoted inside pi prose is not a prompt",
			screen: "func parseYesNo(lines []string) { // handles [y/n] and (y/n)\n",
			kind:   KindPi,
		},
		{
			name: "a marker buried above the bottom of the screen is history",
			screen: `Do you want to continue? [y/n]

step one done
step two done
step three done
step four done
step five done
step six done
step seven done
step eight done
step nine done
step ten done
step eleven done
step twelve done
step thirteen done
`,
			kind: KindCodex,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if d := ParseDialog(tc.screen, tc.kind); d.Usable() {
				t.Errorf("got a usable dialog %+v, want none", d)
			}
		})
	}
}

// A cursor glyph belongs to specific kinds: codex's › must not make a "› 1."
// line a dialog for claude, or a quoted shell prompt would grow buttons.
func TestParseDialogCursorIsKindSpecific(t *testing.T) {
	screen := `› 1. Yes
  2. No
`
	if d := ParseDialog(screen, KindClaude); d.Usable() {
		t.Errorf("claude parsed codex's › cursor: %+v", d)
	}
	if d := ParseDialog(screen, KindCodex); !d.Usable() {
		t.Errorf("codex did not parse its own › cursor")
	}
}

func TestKindMapping(t *testing.T) {
	tests := []struct {
		in           string
		want         Kind
		api          string
		cli          string
		command      string
		needsEmitter bool
	}{
		{"claude", KindClaude, "claude", "claude", "claude", false},
		{"claude-code", KindClaude, "claude", "claude", "claude", false},
		{"Codex", KindCodex, "codex", "codex", "codex", false},
		{"agy", KindAgy, "antigravity_cli", "antigravity-cli", "agy", false},
		{"antigravity-cli", KindAgy, "antigravity_cli", "antigravity-cli", "agy", false},
		{"pi", KindPi, "pi", "pi", "pi", true},
		{"unknown-thing", "", "", "", "", false},
	}
	for _, tc := range tests {
		if got := ParseKind(tc.in); got != tc.want {
			t.Errorf("ParseKind(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// The socket API and the CLI spell Antigravity differently; getting
		// this wrong installs nothing and fails silently.
		if got := tc.want.APITarget(); got != tc.api {
			t.Errorf("%q.APITarget() = %q, want %q", tc.want, got, tc.api)
		}
		if got := tc.want.CLITarget(); got != tc.cli {
			t.Errorf("%q.CLITarget() = %q, want %q", tc.want, got, tc.cli)
		}
		if got := tc.want.Command(); got != tc.command {
			t.Errorf("%q.Command() = %q, want %q", tc.want, got, tc.command)
		}
		if got := tc.want.NeedsStateEmitter(); got != tc.needsEmitter {
			t.Errorf("%q.NeedsStateEmitter() = %v, want %v", tc.want, got, tc.needsEmitter)
		}
	}
}

func TestStatusRoundTrip(t *testing.T) {
	// The five wire values must survive; everything else is unknown, and
	// "exited" must never come from the wire because it closes topics.
	for _, s := range []Status{StatusWorking, StatusIdle, StatusBlocked, StatusDone, StatusUnknown} {
		if got := ParseStatus(string(s)); got != s {
			t.Errorf("ParseStatus(%q) = %q, want %q", s, got, s)
		}
	}
	for _, s := range []string{"exited", "", "waiting", "ERROR"} {
		if got := ParseStatus(s); got != StatusUnknown {
			t.Errorf("ParseStatus(%q) = %q, want unknown", s, got)
		}
	}
	if !StatusBlocked.Waiting() || StatusIdle.Waiting() {
		t.Error("Waiting() must be true only for blocked")
	}
}

func TestCutLabel(t *testing.T) {
	if got := CutLabel("short", 10); got != "short" {
		t.Errorf("CutLabel kept %q", got)
	}
	if got := CutLabel("abcdefghij", 5); got != "abcd…" {
		t.Errorf("CutLabel = %q, want %q", got, "abcd…")
	}
	// Multi-byte labels must be cut on a rune boundary.
	if got := CutLabel("中文标签内容", 3); got != "中文…" {
		t.Errorf("CutLabel = %q, want %q", got, "中文…")
	}
}

// piCursor reproduces the blocked pi pane captured on Herdr 0.9.1 (pane
// w5Z:p9, 2026-09-20), produced by the fixture extension opening
// ctx.ui.confirm.
//
// The indentation is pi's own: everything sits one column in from the pane
// edge, and the unselected option is indented two further columns. That
// relative step, not any absolute indent, is what marks an unselected option,
// so this fixture is also the guard against a parser that hard-codes pi's
// column numbers.
//
// The shape is the whole point: pi does not number its options and marks the
// selected one with →, so the answer is an arrow walk and enter, not a digit.
const piCursor = `















 ────────────────────────────────────────────────────────────────────────────────────────────────────────

 Herdr bridge self-test
 Continue with the bridge test?

 → Yes
   No

 ↑↓ navigate  enter select  escape/ctrl+c cancel

 ────────────────────────────────────────────────────────────────────────────────────────────────────────
 ~/task
 0.0%/1.0M (auto)                                                             (agent-router) deepseek-v4-flash • high
`

// The same dialog rendered deeper in the pane, as a nested or padded layout
// would. The relative rule must still find exactly the two options and the
// question, and must not swallow the question as a third option.
const piCursorDeepIndent = `            ────────────────────────────────────────────────
    Pick a color
    Which one?

      → red
        blue

    ↑↓ navigate  enter select  escape/ctrl+c cancel
    ────────────────────────────────────────────────
`

func TestParseDialogCursorStyle(t *testing.T) {
	d := ParseDialog(piCursor, KindPi)
	if d.Style != StyleCursor {
		t.Fatalf("style = %q, want cursor; choices=%+v", d.Style, d.Choices)
	}
	want := []Choice{
		{Number: 1, Label: "Yes", Key: "1"},
		{Number: 2, Label: "No", Key: "2"},
	}
	if !reflect.DeepEqual(d.Choices, want) {
		t.Errorf("choices = %+v, want %+v", d.Choices, want)
	}
	// state_labels comes back empty for a reported status, so the question has
	// to be recovered from the screen; this is what a Telegram post would say.
	if want := "Herdr bridge self-test · Continue with the bridge test?"; d.Title != want {
		t.Errorf("Title = %q, want %q", d.Title, want)
	}
	// The cursor rests on Yes, so choosing it is just enter and choosing No is
	// one arrow down then enter.
	if d.Cursor != 1 {
		t.Errorf("Cursor = %d, want 1 (Yes is selected)", d.Cursor)
	}
	if got, want := d.KeysFor(d.Choices[0]), []string{"enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(Yes) = %v, want %v", got, want)
	}
	if got, want := d.KeysFor(d.Choices[1]), []string{"down", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(No) = %v, want %v", got, want)
	}
}

func TestParseDialogCursorTracksSelection(t *testing.T) {
	// Three options where the cursor rests on the first, so reaching the last
	// is two presses down. Indentation is what marks the unselected rows.
	const screen = ` Pick one
   → alpha
     beta
     gamma
   ↑↓ navigate  enter select  esc cancel
`
	d := ParseDialog(screen, KindPi)
	if d.Style != StyleCursor {
		t.Fatalf("style = %q, want cursor", d.Style)
	}
	if len(d.Choices) != 3 {
		t.Fatalf("choices = %+v, want 3", d.Choices)
	}
	if d.Cursor != 1 {
		t.Fatalf("Cursor = %d, want 1", d.Cursor)
	}
	if got, want := d.KeysFor(d.Choices[2]), []string{"down", "down", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(gamma) = %v, want %v", got, want)
	}
	if d.Title != "Pick one" {
		t.Errorf("Title = %q, want %q", d.Title, "Pick one")
	}
}

// When the cursor rests on a later row, answering an earlier one must walk up.
func TestParseDialogCursorWalksUp(t *testing.T) {
	const screen = ` Pick one
     alpha
     beta
   → gamma
   ↑↓ navigate  enter select  esc cancel
`
	d := ParseDialog(screen, KindPi)
	if d.Style != StyleCursor {
		t.Fatalf("style = %q, want cursor; choices=%+v", d.Style, d.Choices)
	}
	if d.Cursor != 3 {
		t.Fatalf("Cursor = %d, want 3", d.Cursor)
	}
	if got, want := d.KeysFor(d.Choices[0]), []string{"up", "up", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(alpha) = %v, want %v", got, want)
	}
	if got, want := d.KeysFor(d.Choices[2]), []string{"enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(gamma) = %v, want %v", got, want)
	}
}

// A deeper base column must not change the answer.
func TestParseDialogCursorDeepIndent(t *testing.T) {
	d := ParseDialog(piCursorDeepIndent, KindPi)
	if d.Style != StyleCursor {
		t.Fatalf("style = %q, want cursor; choices=%+v", d.Style, d.Choices)
	}
	if len(d.Choices) != 2 {
		t.Fatalf("choices = %+v, want exactly red and blue", d.Choices)
	}
	if d.Choices[0].Label != "red" || d.Choices[1].Label != "blue" {
		t.Errorf("choices = %+v, want red then blue", d.Choices)
	}
	// The question must not have leaked into the options.
	for _, c := range d.Choices {
		if strings.Contains(c.Label, "Which one") || strings.Contains(c.Label, "Pick a color") {
			t.Errorf("question line became an option: %+v", c)
		}
	}
	if want := "Pick a color · Which one?"; d.Title != want {
		t.Errorf("Title = %q, want %q", d.Title, want)
	}
}

// The cursor style must not fire without its footer, or an indented two-line
// block anywhere on screen would grow buttons.
func TestParseDialogCursorNeedsFooter(t *testing.T) {
	const noFooter = ` Some heading
   alpha
   beta
`
	if d := ParseDialog(noFooter, KindPi); d.Usable() {
		t.Errorf("parsed an option list with no navigate/select footer: %+v", d)
	}
}

// Kinds whose dialogs are numbered must not fall into the cursor style, which
// relies on an indentation heuristic that only pi's measured layout justifies.
func TestParseDialogCursorIsPiOnly(t *testing.T) {
	const screen = ` Continue?
   → Yes
     No
   ↑↓ navigate  enter select  esc cancel
`
	if d := ParseDialog(screen, KindClaude); d.Style == StyleCursor {
		t.Errorf("claude used the cursor style: %+v", d)
	}
	if d := ParseDialog(screen, KindPi); d.Style != StyleCursor {
		t.Errorf("pi did not use the cursor style: %+v", d)
	}
}

// piNumberedCursor is the blocked pi pane measured live on Herdr 0.9.1 (pane
// w5Z:pJ, 2026-09-20). pi numbers this list, so it parses as a numbered block —
// but the digits are labels, not keys: the footer navigates with ↑/↓ and the
// selection carries ❯, so a digit typed into the prompt is just text.
//
// This is the shape that shipped a wrong button: the daemon sent "3", pi read
// it as a message, and the pane stayed blocked. Numbering and arrow navigation
// are not exclusive, and that is what the style decision has to reflect.
const piNumberedCursor = ` Clarify

 "1" is ambiguous to me. What did you mean?

❯ 1. Pick a project to work on
     You'll tell me which of the directories (agy-auto-approver, astrbot, revanced-manager, etc.) to open and I'll start by exploring it.
  2. Continuing a previous list item 1
     You had a numbered task list elsewhere; paste or point me at it and I'll do item 1.
  3. List what's in /Users/example/project
     I'll summarize each directory (purpose, language, git status, README) so you can choose.
  4. Type something.

 ────────────────────────────────────────────────────────────────

 Enter to select · ↑/↓ to navigate · n to add notes · Esc to cancel · Ctrl+] to collapse
`

// A numbered block that a cursor footer closes is an arrow-navigated list, so
// answering walks the cursor instead of sending the digit.
func TestParseDialogNumberedCursorWalks(t *testing.T) {
	d := ParseDialog(piNumberedCursor, KindPi)
	if d.Style != StyleCursor {
		t.Fatalf("style = %q, want cursor; choices=%+v", d.Style, d.Choices)
	}
	want := []Choice{
		{Number: 1, Label: "Pick a project to work on", Caption: "1. Pick a project to work on", Key: "1"},
		{Number: 2, Label: "Continuing a previous list item 1", Caption: "2. Continuing a previous list item 1", Key: "2"},
		{Number: 3, Label: "List what's in /Users/example/project", Caption: "3. List what's in /Users/example/project", Key: "3"},
		{Number: 4, Label: "Type something", Caption: "4. Type something.", Key: "4"},
	}
	if !reflect.DeepEqual(d.Choices, want) {
		t.Errorf("choices = %+v, want %+v", d.Choices, want)
	}
	// The cursor rests on the first option, so the third is two rows down.
	if d.Cursor != 1 {
		t.Errorf("Cursor = %d, want 1", d.Cursor)
	}
	if got, want := d.KeysFor(d.Choices[2]), []string{"down", "down", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(option 3) = %v, want %v", got, want)
	}
	if got, want := d.KeysFor(d.Choices[0]), []string{"enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KeysFor(option 1) = %v, want %v", got, want)
	}
	// The description lines are option prose, not options of their own.
	for _, c := range d.Choices {
		if strings.Contains(c.Label, "I'll summarize") || strings.Contains(c.Label, "You'll tell me") {
			t.Errorf("description became an option: %+v", c)
		}
	}
	if !strings.Contains(d.Title, "ambiguous") {
		t.Errorf("Title = %q, want the question above the options", d.Title)
	}
}

// Without the cursor glyph the numbered reading stands: a plain numbered block
// on pi keeps sending digits.
func TestParseDialogNumberedWithoutCursorStaysNumbered(t *testing.T) {
	const screen = `You have unsaved changes.

  1. Save and continue
  2. Discard
`
	if d := ParseDialog(screen, KindPi); d.Style != StyleNumbered {
		t.Fatalf("style = %q, want numbered", d.Style)
	}
}

func TestTrimScreenChrome(t *testing.T) {
	const codexScreen = `Would you like to run the following command?

  $ printf 'hello' > /tmp/test.txt

› 1. Yes, proceed (y)
  2. No, cancel (esc)

  Press enter to confirm or esc to cancel

› Ask Codex to do anything

  gpt-5.6-luna high · ~/task/herdr-telegram-bridge · Context 0% used
`
	wantCodex := `Would you like to run the following command?

  $ printf 'hello' > /tmp/test.txt

› 1. Yes, proceed (y)
  2. No, cancel (esc)`

	if got := TrimScreenChrome(codexScreen); got != wantCodex {
		t.Errorf("TrimScreenChrome(codex):\ngot:\n%q\nwant:\n%q", got, wantCodex)
	}

	const piScreen = `Herdr bridge self-test
Continue with the bridge test?

→ Yes
  No

↑↓ navigate  enter select  escape/ctrl+c cancel
`
	wantPi := `Herdr bridge self-test
Continue with the bridge test?

→ Yes
  No`

	if got := TrimScreenChrome(piScreen); got != wantPi {
		t.Errorf("TrimScreenChrome(pi):\ngot:\n%q\nwant:\n%q", got, wantPi)
	}

	const piModernDoneScreen = ` $ curl -sL "https://example.com"
 Took 54.2s

── ⠏ Working ──────────────────────────────────────────────────────────────────
────────────────────────────────────────────────────────────────────────────────
~/task
↑23k ↓3.1k R61k CH0.0% 2.3%/1.0M (auto)     (cline) deepseek-v4.1-flash • high
● ADHD ON yolo ● 🐴 ponytail: ⚡ FULL
`
	wantModernPi := ` $ curl -sL "https://example.com"
 Took 54.2s`
	if got := TrimScreenChrome(piModernDoneScreen); got != wantModernPi {
		t.Errorf("TrimScreenChrome(piModernDoneScreen):\ngot:\n%q\nwant:\n%q", got, wantModernPi)
	}
}
