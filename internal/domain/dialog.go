package domain

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Choice is one selectable option of a dialog an agent drew on its screen.
type Choice struct {
	// Number is the digit the agent expects, 1..9. Zero for a choice that
	// is answered with a key rather than a digit (the yes/no fallback).
	Number int
	// Label is the option text with its number, cursor and checkbox
	// stripped, for the button caption.
	Label string
	// Caption preserves a numbered option's original text, without its cursor.
	// Empty for unnumbered options, whose Label already is the display text.
	Caption string
	// Key is what agent.send_keys must receive: the digit for a numbered
	// choice, "y"/"n" for a yes/no prompt.
	Key string
}

// Style is how the dialog is answered.
type Style string

const (
	// StyleNone means the screen ends in no dialog this plugin can read.
	StyleNone Style = ""
	// StyleNumbered is "1. Yes / 2. No" and friends: the digit is the answer.
	StyleNumbered Style = "numbered"
	// StyleYesNo is a "[y/n]" or "allow command?" prompt with no numbering;
	// the plugin draws two buttons that send "y" and "n".
	StyleYesNo Style = "yesno"
	// StyleCursor is an unnumbered option list navigated with the arrow keys,
	// where the answer is a number of arrow presses followed by enter. pi draws
	// this shape and nothing else, so without it a blocked pi pane produces no
	// buttons at all.
	StyleCursor Style = "cursor"
	// StyleText is an open free-text question, without selectable choices.
	StyleText Style = "text"
	// StyleQueued represents a collapsed question that needs opening.
	StyleQueued Style = "queued"
)

// Dialog is the dialog at the bottom of a blocked screen: the options that
// become buttons, plus the facts needed to answer a multi-select one.
type Dialog struct {
	Kind  Kind
	Style Style
	// Title is the prompt text above the options, joined into one line.
	//
	// It matters because AgentInfo.state_labels comes back empty for a status
	// reported through pane.report_agent: Herdr fills that map from its own
	// detection manifests, not from the `message` a self-reporting agent sends
	// (measured on 0.9.1 with a blocked pi pane: state_labels was {} while the
	// reporter had sent "confirm: Herdr bridge self-test"). So the question has
	// to come off the screen after all, and this is it.
	Title   string
	Choices []Choice
	// Multi is set when every option opens with a checkbox glyph, meaning
	// the agent toggles an option per digit and submits with enter.
	Multi bool
	// TextEntry is the number of the entry that opens a free-text answer
	// ("Type something.", "Chat about this"), or 0 when there is none.
	TextEntry int
	// TextLabel is that entry's text without its trailing period.
	TextLabel string
	// Cursor is the navigable row the cursor glyph sits on, counting every
	// numbered entry and a bare "Submit" row from 1 in screen order; 0 when
	// no cursor is drawn. Used to walk to SubmitRow.
	Cursor int
	// SubmitRow is the row of the bare "Submit" line a multi-select dialog
	// ends with, or 0 when the renderer submits on enter.
	SubmitRow int
	// Body is a scoped question panel, excluding unrelated terminal history.
	Body string
	// TextInput means the panel accepts a typed answer directly.
	TextInput bool
}

// Usable reports whether the dialog produced buttons.
func (d Dialog) Usable() bool {
	return d.Style == StyleText || d.Style == StyleQueued || (d.Style != StyleNone && len(d.Choices) > 0)
}

// MaxChoiceKeys is the highest digit an option may carry and still be
// answerable: agent.send_keys understands 1..9, so a dialog with a tenth
// option gets no buttons and is answered by typing instead.
const MaxChoiceKeys = 9

// maxChoiceRows bounds the block scan so a pathological screen cannot make
// parsing quadratic.
const maxChoiceRows = 60

// maxTitleLines is how many lines above an option block are joined into the
// dialog title. pi draws two (a headline and the question); one more is allowed
// without risking a paragraph being swallowed as a prompt.
const maxTitleLines = 3

// yesNoScanLines is how many of the bottom non-empty lines a yes/no marker may
// appear in. A prompt is drawn at the bottom; anything higher is history.
const yesNoScanLines = 12

// Profile holds the screen conventions of one agent kind. Every field has a
// working default so the zero Profile parses a plain numbered dialog; kinds
// only override what differs.
type Profile struct {
	Kind Kind
	// Cursors are the glyphs an agent puts in front of the highlighted row.
	// Claude Code draws ❯, Codex draws ›.
	Cursors []string
	// TextEntryLabels are service entries that open free text. They are
	// dropped from the options but keep their number on the rest, so the
	// digit sent is always the one the agent shows.
	TextEntryLabels []string
	// Footers are substrings that may trail the dialog block without
	// ending it.
	Footers []string
	// YesNo enables the unnumbered yes/no fallback. Both substrings must
	// appear in the tail for it to fire.
	YesNo []string
	// CursorOptions enables the arrow-navigated option list (StyleCursor) that
	// pi draws. It is off for kinds whose dialogs are numbered, because the
	// indent heuristic it needs would otherwise be able to fire on prose.
	CursorOptions bool
	// CursorFooters are substrings that must all occur in the hint line that
	// closes a cursor option list ("navigate", "select").
	CursorFooters []string
	// CursorFooterAny contains alternative footer forms. At least one must
	// occur when this is non-empty.
	CursorFooterAny []string
}

// claudeProfile is measured from Herdr's own claude detection manifest
// (agent-detection/remote/claude.toml, version 2026.09.11.1).
var claudeProfile = Profile{
	Kind:            KindClaude,
	Cursors:         []string{"❯"},
	TextEntryLabels: []string{"Type something", "Chat about this"},
	Footers: []string{
		"enter to select", "enter to confirm", "esc to cancel",
		"tab/arrow keys to navigate", "arrow keys to navigate",
		"arrows to navigate", "↑/↓ to navigate", "↑↓ to navigate",
		"tab to amend", "ctrl+e to explain",
	},
}

// codexProfile is measured from codex.toml (version 2026.09.14.1). Codex
// marks the selected row with › rather than ❯ and asks trust prompts with
// a leading ">" line.
var codexProfile = Profile{
	Kind:          KindCodex,
	Cursors:       []string{"›", "❯"},
	CursorOptions: true,
	CursorFooterAny: []string{
		"press enter to confirm or esc to cancel",
		"enter to submit answer", "enter to submit all",
	},
	Footers: []string{
		"press enter to confirm or esc to cancel",
		"enter to submit answer", "enter to submit all",
		"esc to cancel", "esc to interrupt",
	},
	YesNo: []string{"[y/n]", "yes (y)"},
}

// agyProfile is measured from agy.toml (version 2026.06.24.1), which only
// guarantees the "requesting permission for:" header and a
// "do you want to proceed?" / "tab amend" body.
var agyProfile = Profile{
	Kind:    KindAgy,
	Cursors: []string{"❯", "›", ">"},
	Footers: []string{"do you want to proceed?", "tab amend", "edit command", "esc to cancel"},
	YesNo:   []string{"[y/n]", "(y/n)", "yes/no"},
}

// piProfile is measured from a real blocked pi pane.
//
// Herdr's pi manifest (version 2026.09.14.1) declares no blocked rule at all,
// so pi only reaches blocked through this plugin's companion pi extension, and
// this profile is the only place pi's dialog shape is written down.
//
// Captured from pane w5Z:p9 on 2026-09-20, after the fixture extension opened
// ctx.ui.confirm:
//
//	Herdr bridge self-test
//	Continue with the bridge test?
//
//	→ Yes
//	  No
//
//	↑↓ navigate  enter select  escape/ctrl+c cancel
//
// The options are unnumbered and the selected one is marked with →, so the
// answer is an arrow walk and enter rather than a digit.
var piProfile = Profile{
	Kind:          KindPi,
	Cursors:       []string{"→", "❯", "›", ">"},
	CursorOptions: true,
	CursorFooters: []string{"navigate", "select"},
	Footers:       []string{"esc to cancel", "esc to interrupt", "enter to confirm"},
	YesNo:         []string{"[y/n]", "(y/n)"},
}

// genericProfile parses any numbered dialog with no kind knowledge.
var genericProfile = Profile{Cursors: []string{"❯", "›", ">"}}

// ProfileFor returns the profile of a kind, or the generic one for a kind
// this plugin has no measurements for.
func ProfileFor(k Kind) Profile {
	switch k {
	case KindClaude:
		return claudeProfile
	case KindCodex:
		return codexProfile
	case KindAgy:
		return agyProfile
	case KindPi:
		return piProfile
	default:
		return genericProfile
	}
}

var (
	// numberedItem matches "1. Label" / "2) Label" after an optional
	// kind-specific cursor has been removed.
	numberedItem = regexp.MustCompile(`^(\d{1,2})[.)]\s+(\S.*?)\s*$`)
	// checkboxGlyph matches the multi-select marker: "[ ]", "[x]", "[✔]"
	// with or without a variation selector, or the bare ☐/☑/☒ pair.
	checkboxGlyph = regexp.MustCompile(`^(?:\[[^\[\]]{1,2}\]|\[ \]|[☐☑☒])\s+`)
	// ruleLine matches a horizontal rule: at least six of the same box or
	// dash character.
	ruleLine = regexp.MustCompile(`^\s*[─═━—–-]{6,}\s*$`)
	// bannerRuleLine matches progress/status banners bounded by dashes:
	// "── ⠏ Working ───────────────────────"
	bannerRuleLine = regexp.MustCompile(`(?i)^\s*[─═━—–-]{2,}.*(?:Working|Thinking|Idle|Ready|Done).*[─═━—–-]{2,}\s*$`)

	// inputPromptLine matches an empty prompt or placeholder composer line:
	// "› Ask Codex...", "›", "❯", ">", "π", "%", "$", "#", "?"
	inputPromptLine = regexp.MustCompile(`^\s*(?:[›❯>π]\s*(?:Ask Codex|Type a message|Type something|Chat about).*|[›❯>π?$%#])\s*$`)
	// shellPromptLine matches user@host:dir % or similar terminal prompts
	shellPromptLine = regexp.MustCompile(`^\s*[\w\.\-]+@[\w\.\-]+[:\s].*[%$#]\s*$`)
	// statusContextLine matches Codex/Claude status lines like "gpt-... · ... · Context 0% used"
	statusContextLine = regexp.MustCompile(`(?i)(?:context\s+\d+%\s+used|high\s*·\s*~|low\s*·\s*~|medium\s*·\s*~)`)
	// piStatusLine matches pi's context window gauge, e.g. "2.3%/1.0M (auto)"
	// or preceded by transfer/token stats "↑23k ↓3.1k R61k CH0.0% 2.3%/1.0M (auto)..."
	piStatusLine = regexp.MustCompile(`\d+(?:\.\d+)?%/\d+(?:\.\d+)?[kKmM]\s+\(auto\)`)
	// piExtensionLine matches extension status badges, e.g. "● ADHD ON yolo ● 🐴 ponytail: ⚡ FULL"
	piExtensionLine = regexp.MustCompile(`^\s*●\s+.*`)
	// cwdLine matches standalone working directory or branch lines at the bottom of a screen
	cwdLine = regexp.MustCompile(`^\s*(?:~|/|[A-Za-z]:[/\\])\S*(?:\s+\([^)]+\))?\s*$`)

	allKnownFooters = []string{
		"press enter to confirm or esc to cancel",
		"press enter to confirm",
		"press enter to continue",
		"enter to confirm or esc to cancel",
		"enter to confirm",
		"enter to select",
		"enter to submit answer",
		"enter to submit all",
		"enter to submit",
		"esc to cancel",
		"esc to interrupt",
		"arrow keys to navigate",
		"arrows to navigate",
		"tab/arrow keys to navigate",
		"↑/↓ to navigate",
		"↑↓ to navigate",
		"↑↓ navigate",
		"ctrl+e to explain",
		"tab to amend",
		"edit command",
	}
)

// TrimTrailingPrompts strips trailing input/composer lines (e.g. "› Ask Codex...",
// shell prompts), and status bars from the bottom of terminal screen output,
// while preserving dialog footer lines needed by the parser.
func TrimTrailingPrompts(screen string) string {
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	for len(lines) > 0 {
		trimmed := strings.TrimSpace(lines[len(lines)-1])
		if trimmed == "" || isPromptOrStatusLine(trimmed) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}

func isPromptOrStatusLine(trimmed string) bool {
	// Codex/pi draws braille animation/spinners across composers or banners.
	// Remove it only for chrome classification, never from the actual question or output.
	trimmed = strings.TrimSpace(withoutComposerAnimation(trimmed))
	if trimmed == "" {
		return true
	}
	return inputPromptLine.MatchString(trimmed) ||
		shellPromptLine.MatchString(trimmed) ||
		statusContextLine.MatchString(trimmed) ||
		piStatusLine.MatchString(trimmed) ||
		piExtensionLine.MatchString(trimmed) ||
		cwdLine.MatchString(trimmed) ||
		ruleLine.MatchString(trimmed) ||
		bannerRuleLine.MatchString(trimmed)
}

func withoutComposerAnimation(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '\u2800' && r <= '\u28ff' {
			return -1
		}
		return r
	}, s)
}

// TrimScreenChrome strips trailing footer lines (e.g. "press enter to confirm"),
// input/composer lines (e.g. "› Ask Codex...", shell prompts), and status bars
// from the bottom of terminal screen output.
func TrimScreenChrome(screen string) string {
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	for len(lines) > 0 {
		trimmed := strings.TrimSpace(lines[len(lines)-1])
		if trimmed == "" || isChromeLine(trimmed) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}

func isChromeLine(trimmed string) bool {
	if codexAnswerHint.MatchString(trimmed) || codexQuestionCount.MatchString(trimmed) || trimmed == codexQueueHeading {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, f := range allKnownFooters {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return isPromptOrStatusLine(trimmed)
}

// ParseDialog reads the dialog at the bottom of a blocked screen. It
// returns the zero Dialog when the screen ends in no dialog it can read;
// callers then post the screen without buttons and let the operator answer
// by typing.
//
// The dialog is the bottom-most block that starts at a "1." item and runs
// to the end of the screen with nothing after it but fillers: further items
// numbered 1, 2, 3, … without gaps, blank lines, rules, indented
// descriptions, the kind's footers and a bare "Submit" row. Anything else
// after the block means those numbers were a list in the transcript rather
// than a dialog, which is the failure mode that makes a naive parser put
// buttons on an agent's prose.
func ParseDialog(screen string, kind Kind) Dialog {
	prof := ProfileFor(kind)
	// A shell prompt means the agent has exited; never revive an old dialog.
	if lines := screenLines(strings.TrimSpace(screen)); len(lines) > 0 && shellPromptLine.MatchString(lines[len(lines)-1]) {
		return Dialog{}
	}
	screen = TrimTrailingPrompts(screen)
	if kind == KindCodex {
		if body := codexPendingBody(screen); body != "" {
			return Dialog{Kind: kind, Style: StyleQueued, Title: codexQueueHeading, Body: body}
		}
		if dialog, ok := parseCodexQuestion(screen); ok {
			return dialog
		}
	}
	lines := screenLines(screen)
	if len(lines) > maxChoiceRows {
		lines = lines[len(lines)-maxChoiceRows:]
	}
	// Trailing blanks shift the block start for no benefit.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return Dialog{}
	}
	if d, ok := parseNumbered(lines, prof); ok {
		return d
	}
	if d, ok := parseCursorOptions(lines, prof); ok {
		return d
	}
	if d, ok := parseYesNo(lines, prof); ok {
		return d
	}
	return Dialog{}
}

// parseCursorOptions reads the unnumbered, arrow-navigated option list pi
// draws, e.g.
//
//	Herdr bridge self-test
//	Continue with the bridge test?
//
//	→ Yes
//	  No
//
//	↑↓ navigate  enter select  escape/ctrl+c cancel
//
// The dialog is located by its footer rather than by its options. The footer
// naming navigation and selection is the only thing that distinguishes two
// indented lines from a real choice, and requiring it is what keeps an
// arbitrary two-line indented block from growing buttons.
//
// Options are collected upwards from the footer until a line that cannot be an
// option: the selected one carries a cursor glyph, the rest are only indented
// (pi indents them under the cursor). The first non-option line above the block
// carries the prompt text.
func parseCursorOptions(lines []string, prof Profile) (Dialog, bool) {
	if !prof.CursorOptions || (len(prof.CursorFooters) == 0 && len(prof.CursorFooterAny) == 0) {
		return Dialog{}, false
	}
	footer := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if isCursorFooter(lines[i], prof) {
			footer = i
			break
		}
	}
	if footer < 0 {
		return Dialog{}, false
	}
	for i := footer + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || ruleLine.MatchString(line) {
			continue
		}
		if prof.Kind == KindPi && (piStatusLine.MatchString(line) ||
			(strings.HasPrefix(line, "~/") && i+1 < len(lines) && piStatusLine.MatchString(strings.TrimSpace(lines[i+1])))) {
			continue
		}
		return Dialog{}, false
	}

	// The option block is the run of non-blank lines directly above the
	// footer. Everything above its leading blank is prompt text.
	end := footer - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	start := end
	for start >= 0 && strings.TrimSpace(lines[start]) != "" && !ruleLine.MatchString(lines[start]) {
		start--
	}
	block := lines[start+1 : end+1]
	if len(block) == 0 {
		return Dialog{}, false
	}

	// The cursor line is the one unambiguous anchor in the block: it is the
	// selected option, and its indentation is the dialog's own base column.
	// Unselected options are indented *relative to it*.
	//
	// Using an absolute indent threshold instead would misfire whenever a pane
	// renders the dialog at a deeper base column than the one pi happens to
	// use today, and the question line above would be swallowed as an option.
	base, cursorAt := -1, -1
	for i, line := range block {
		if _, found := cursorPrefix(line, prof); found {
			base = leadingIndent(line)
			cursorAt = i
			break
		}
	}
	if cursorAt < 0 {
		// Without a cursor there is no base column, and guessing one is how
		// prose grows buttons. Decline.
		return Dialog{}, false
	}

	// Options are collected outwards from the cursor line, not from the top of
	// the block. The cursor is the one line whose role is certain, and pi can
	// draw it in the middle of the list, so options exist both above and below
	// it. Walking from the top instead would treat a prompt line that happens
	// to sit inside the block (any layout without a blank line before the
	// options) as the first option and stop.
	cursorLabel, _ := cursorPrefix(block[cursorAt], prof)

	// Below the cursor.
	below := []string{cursorLabel}
	for i := cursorAt + 1; i < len(block); i++ {
		if leadingIndent(block[i]) <= base {
			break
		}
		if label := strings.TrimSpace(block[i]); label != "" && !ruleLine.MatchString(block[i]) {
			below = append(below, label)
		}
	}

	// Above the cursor, collected upwards then reversed into screen order.
	var above []string
	for i := cursorAt - 1; i >= 0; i-- {
		if leadingIndent(block[i]) <= base {
			break
		}
		if label := strings.TrimSpace(block[i]); label != "" && !ruleLine.MatchString(block[i]) {
			above = append(above, label)
		}
	}
	for l, r := 0, len(above)-1; l < r; l, r = l+1, r-1 {
		above[l], above[r] = above[r], above[l]
	}

	ordered := append(append([]string{}, above...), below...)
	if len(ordered) < minChoices || len(ordered) > MaxChoiceKeys {
		return Dialog{}, false
	}
	choices := make([]Choice, 0, len(ordered))
	for i, label := range ordered {
		// Numbered lists must pass readBlock's validation, not this fallback.
		if numberedItem.MatchString(label) {
			return Dialog{}, false
		}
		choices = append(choices, Choice{
			Number: i + 1,
			Label:  label,
			Key:    strconv.Itoa(i + 1),
		})
	}

	// The cursor sits one past the options that preceded it.
	cursor := len(above) + 1

	// The prompt is whatever sits above the topmost option. That is not always
	// the line before the block: when a layout draws the question with no blank
	// line before the options, the question is inside the block and the block's
	// own leading index would point past it.
	optionsTop := start + 1 + cursorAt - len(above)

	return Dialog{
		Kind:    prof.Kind,
		Style:   StyleCursor,
		Title:   promptAbove(lines, optionsTop-1, prof),
		Choices: choices,
		Cursor:  cursor,
	}, true
}

// cursorPrefix reports whether a line opens with one of the kind's cursor
// glyphs, returning the label after it.
func cursorPrefix(line string, prof Profile) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	for _, c := range prof.Cursors {
		if after, ok := strings.CutPrefix(trimmed, c); ok {
			after = strings.TrimSpace(after)
			if after == "" {
				return "", false
			}
			return after, true
		}
	}
	return "", false
}

// promptAbove collects the prompt text sitting above an option block, where i
// is the index of the last line before the block's leading blank.
//
// Up to maxTitleLines non-blank lines are joined into one, in screen order, so
// pi's two-line "<headline>\n<question>" reads as one summary. A rule or a
// numbered item ends the walk, so a line from a previous block is not mistaken
// for the current question.
func promptAbove(lines []string, i int, prof Profile) string {
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	var collected []string
	for ; i >= 0 && len(collected) < maxTitleLines; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || ruleLine.MatchString(line) {
			break
		}
		if _, _, isItem := parseItem(line, prof); isItem {
			break
		}
		collected = append(collected, trimmed)
	}
	// collected is bottom-up; join in screen order.
	for l, r := 0, len(collected)-1; l < r; l, r = l+1, r-1 {
		collected[l], collected[r] = collected[r], collected[l]
	}
	return strings.Join(collected, " · ")
}

// isCursorFooter reports whether a line is the hint line that closes a cursor
// option list.
func isCursorFooter(line string, prof Profile) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	for _, f := range prof.CursorFooters {
		if !strings.Contains(lower, f) {
			return false
		}
	}
	if len(prof.CursorFooterAny) > 0 {
		found := false
		for _, f := range prof.CursorFooterAny {
			if strings.Contains(lower, f) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// parseNumbered scans for the bottom-most readable numbered block.
func parseNumbered(lines []string, prof Profile) (Dialog, bool) {
	// Candidate starts, bottom-most (i.e. latest) first: a line that reads
	// as item number 1.
	for start := len(lines) - 1; start >= 0; start-- {
		if n, _, ok := parseItem(lines[start], prof); !ok || n != 1 {
			continue
		}
		if d, ok := readBlock(lines[start:], prof); ok {
			d.Title = promptAbove(lines, start-1, prof)
			// pi numbers its arrow-navigated list, so those digits are labels
			// rather than keys. A cursor glyph plus a navigation footer means
			// the answer is an arrow walk: sending the digit types it into the
			// prompt instead (measured live on w5Z:pJ, 2026-09-20 — "3" went in
			// as text and the pane stayed blocked).
			if prof.CursorOptions && d.Cursor > 0 && hasCursorFooter(lines[start:], prof) {
				d.Style = StyleCursor
			}
			return d, true
		}
	}
	return Dialog{}, false
}

// hasCursorFooter reports whether a navigation hint line closes a block.
func hasCursorFooter(lines []string, prof Profile) bool {
	for _, line := range lines {
		if isCursorFooter(line, prof) {
			return true
		}
	}
	return false
}

// blockItem is one numbered entry of a candidate block.
//
// label is the display text with cursor and checkbox removed; boxed records
// whether a checkbox was there, because stripping it is what makes the
// multi-select flag unrecoverable later.
type blockItem struct {
	number  int
	label   string
	caption string
	boxed   bool
	service bool // opened free text rather than choosing
}

// readBlock validates one candidate block and builds the dialog from it.
func readBlock(block []string, prof Profile) (Dialog, bool) {
	var items []blockItem
	rows, cursor, submitRow := 0, 0, 0

	for _, line := range block {
		n, raw, ok := parseItem(line, prof)
		if ok {
			// Numbers must be 1, 2, 3, … with no gap, or this is prose.
			if n != len(items)+1 {
				return Dialog{}, false
			}
			rows++
			if hasCursor(line, prof) {
				cursor = rows
			}
			stripped := stripCheckbox(raw)
			label := strings.TrimSuffix(stripped, ".")
			caption := strings.TrimSpace(line)
			if withoutCursor, ok := cursorPrefix(line, prof); ok {
				caption = withoutCursor
			}
			items = append(items, blockItem{
				number:  n,
				label:   label,
				caption: caption,
				boxed:   stripped != raw,
				service: isTextEntryLabel(label, prof),
			})
			continue
		}
		if isSubmitRow(line, prof) {
			rows++
			submitRow = rows
			if hasCursor(line, prof) {
				cursor = rows
			}
			continue
		}
		// A numbered line whose number cannot be answered ends the block
		// rather than being swallowed as an indented description, so a ten
		// option dialog yields no buttons instead of nine wrong ones.
		if it, shaped := matchItem(line, prof); shaped && (it.number < 1 || it.number > MaxChoiceKeys) {
			return Dialog{}, false
		}
		if !isFiller(line, prof) {
			return Dialog{}, false
		}
	}

	// Service entries keep their number on the others but become no button;
	// the first one is the free-text row the ✏️ button targets.
	choices := make([]Choice, 0, len(items))
	multi, sawReal := true, false
	textEntry, textLabel := 0, ""
	for _, it := range items {
		if it.service {
			if textEntry == 0 {
				textEntry, textLabel = it.number, it.label
			}
			continue
		}
		sawReal = true
		if !it.boxed {
			multi = false
		}
		choices = append(choices, Choice{
			Number:  it.number,
			Label:   it.label,
			Caption: it.caption,
			Key:     strconv.Itoa(it.number),
		})
	}
	if !sawReal || len(choices) < minChoices || len(choices) > MaxChoiceKeys {
		return Dialog{}, false
	}
	return Dialog{
		Kind:      prof.Kind,
		Style:     StyleNumbered,
		Choices:   choices,
		Multi:     multi,
		TextEntry: textEntry,
		TextLabel: textLabel,
		Cursor:    cursor,
		SubmitRow: submitRow,
	}, true
}

// minChoices is the fewest options that count as a dialog. A lone "1." is
// far more likely a numbered list in the transcript.
const minChoices = 2

// itemMatch is a line that is shaped like a numbered option of a kind.
type itemMatch struct {
	number int
	label  string
}

// matchItem reports whether a line is shaped like "N. Label" for this kind,
// with an optional cursor glyph the kind actually draws. The number range is
// deliberately not checked here: readBlock needs to tell "this number is out
// of range, so the block ends" apart from "this line is prose".
func matchItem(line string, prof Profile) (itemMatch, bool) {
	trimmed := strings.TrimSpace(line)
	for _, cursor := range prof.Cursors {
		if rest, ok := strings.CutPrefix(trimmed, cursor); ok {
			trimmed = strings.TrimSpace(rest)
			break
		}
	}
	m := numberedItem.FindStringSubmatch(trimmed)
	if m == nil {
		return itemMatch{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return itemMatch{}, false
	}
	return itemMatch{number: n, label: strings.TrimSpace(m[2])}, true
}

// parseItem is matchItem with the answerable range applied.
func parseItem(line string, prof Profile) (int, string, bool) {
	it, ok := matchItem(line, prof)
	if !ok || it.number < 1 || it.number > MaxChoiceKeys {
		return 0, "", false
	}
	return it.number, it.label, true
}

// hasCursor reports whether the line carries one of the kind's cursors in
// front of its content.
func hasCursor(line string, prof Profile) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false
	}
	for _, c := range prof.Cursors {
		if strings.HasPrefix(trimmed, c) {
			return true
		}
	}
	return false
}

// isSubmitRow reports whether the line is the bare "Submit" row a
// multi-select dialog ends its entries with.
func isSubmitRow(line string, prof Profile) bool {
	trimmed := strings.TrimSpace(line)
	for _, c := range prof.Cursors {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, c))
	}
	return trimmed == "Submit"
}

func isTextEntryLabel(label string, prof Profile) bool {
	for _, s := range prof.TextEntryLabels {
		if strings.EqualFold(label, s) {
			return true
		}
	}
	return false
}

// isFiller reports whether a non-item line may sit inside or after the
// dialog block: blank, a rule, an indented description, one of the kind's
// footers, or a pure-decoration line. Everything else ends the block.
func isFiller(line string, prof Profile) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return true
	case ruleLine.MatchString(line):
		return true
	case isFooter(trimmed, prof):
		return true
	case leadingIndent(line) >= 2:
		// Agents indent option descriptions under their option. Prose is
		// not indented, so this is safe and it is what lets claude's
		// multi-select descriptions through.
		return true
	}
	return false
}

func isFooter(trimmed string, prof Profile) bool {
	lower := strings.ToLower(trimmed)
	for _, f := range prof.Footers {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

// leadingIndent counts the leading spaces/tabs of a line, counting a tab as
// two columns.
func leadingIndent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 2
		default:
			return n
		}
	}
	return n
}

func stripCheckbox(label string) string {
	if loc := checkboxGlyph.FindStringIndex(label); loc != nil {
		return strings.TrimSpace(label[loc[1]:])
	}
	return label
}

// parseYesNo handles prompts with no numbering: Codex's "[y/n]", an
// "allow command?" line, and the "(y/n)" shapes agy and pi use.
//
// Two guards keep it from firing on prose, both of which were needed in
// practice. First, the marker must *end* a line rather than appear anywhere in
// the screen: an agent's output routinely contains "[y/n]" while explaining
// something, and a plain substring test grew Yes/No buttons on this very
// plugin's own source files when they were displayed in a pane. Second, only
// the bottom of the screen is considered, because a prompt is drawn at the
// bottom, so a marker further up belongs to something already finished.
func parseYesNo(lines []string, prof Profile) (Dialog, bool) {
	if len(prof.YesNo) == 0 {
		return Dialog{}, false
	}

	// Scan the last few non-empty lines, bottom first.
	scanned, markerAt := 0, -1
	for i := len(lines) - 1; i >= 0 && scanned < yesNoScanLines; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			scanned++
			if markerAt < 0 {
				candidate := strings.ToLower(strings.TrimSpace(lines[i]))
				candidate = strings.TrimRight(candidate, " \t.?!:;,")
				for _, marker := range prof.YesNo {
					if before, ok := strings.CutSuffix(candidate, marker); ok {
						before = strings.TrimSpace(before)
						if before == "" || strings.HasSuffix(before, "?") {
							markerAt = i
							break
						}
					}
				}
			}
		}
	}

	if markerAt < 0 {
		return Dialog{}, false
	}
	return Dialog{
		Kind:  prof.Kind,
		Style: StyleYesNo,
		Title: promptAbove(lines, markerAt-1, prof),
		Choices: []Choice{
			{Label: "Yes", Key: "y"},
			{Label: "No", Key: "n"},
		},
	}, true
}

// screenLines splits a screen into lines with CRLF normalised and trailing
// whitespace kept (indentation drives the filler rules).
func screenLines(screen string) []string {
	return strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
}

// SelectKeys returns the keys that answer a cursor dialog with option n: walk
// the cursor from where it rests to that row, then confirm.
func (d Dialog) SelectKeys(n int) []string {
	if d.Style != StyleCursor {
		return nil
	}
	if n < 1 || n > len(d.Choices) {
		return nil
	}
	cursor := d.Cursor
	if cursor < 1 || cursor > len(d.Choices) {
		cursor = 1
	}
	if cursor == n {
		return []string{KeyEnter}
	}
	key := KeyDown
	steps := n - cursor
	if steps < 0 {
		key = KeyUp
		steps = -steps
	}
	out := make([]string, 0, steps+1)
	for i := 0; i < steps; i++ {
		out = append(out, key)
	}
	return append(out, KeyEnter)
}

// KeysFor returns the key sequence that answers with a choice, whichever way
// the kind expects to be answered. Callers should use this rather than reading
// Choice.Key, so a cursor dialog is not answered with a digit the agent would
// type into its prompt box.
func (d Dialog) KeysFor(c Choice) []string {
	if d.Style == StyleCursor {
		return d.SelectKeys(c.Number)
	}
	if c.Key == "" {
		return nil
	}
	return []string{c.Key}
}

// SubmitKeys returns the key sequence that submits a multi-select dialog.
// Without a Submit row the renderer submits on enter. With one, the arrows
// walk from the cursor row (or row 1 when none is drawn) to the Submit row
// and enter confirms.
func (d Dialog) SubmitKeys() []string {
	if d.SubmitRow == 0 {
		return []string{KeyEnter}
	}
	cursor := d.Cursor
	if cursor == 0 {
		cursor = 1
	}
	if cursor == d.SubmitRow {
		return []string{KeyEnter}
	}
	key := KeyDown
	if cursor > d.SubmitRow {
		key = KeyUp
	}
	steps := cursor - d.SubmitRow
	if steps < 0 {
		steps = -steps
	}
	out := make([]string, 0, steps+1)
	for i := 0; i < steps; i++ {
		out = append(out, key)
	}
	return append(out, KeyEnter)
}

// Key names Herdr accepts for the arrows and enter.
const (
	KeyEnter  = "enter"
	KeyDown   = "down"
	KeyUp     = "up"
	KeyEscape = "esc"
	KeyCtrlC  = "ctrl+c"
)

// ToggleKeys returns the keys that tick option number n in a multi-select
// dialog: the digit toggles without moving the cursor, so the digit alone
// is enough.
func (d Dialog) ToggleKeys(n int) []string {
	if n < 1 || n > MaxChoiceKeys {
		return nil
	}
	return []string{strconv.Itoa(n)}
}

// CutLabel shortens a label to at most n runes, ending in an ellipsis when
// it had to cut. Telegram caps button text at 64 bytes; n is in runes, so
// callers should keep n conservative for CJK labels.
func CutLabel(label string, n int) string {
	if n <= 0 || utf8.RuneCountInString(label) <= n {
		return label
	}
	runes := []rune(label)
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}
