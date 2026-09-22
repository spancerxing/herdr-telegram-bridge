package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// question is one dialog posted to a topic: the message carrying its
// buttons, and the parsed dialog those buttons answer.
type question struct {
	messageID int
	dialog    domain.Dialog
}

// typingWait is the ✏️ state: the agent is waiting for free text, and the
// next operator message in the topic is that text.
type typingWait struct {
	messageID int
	until     time.Time
}

const typingWaitTTL = 10 * time.Minute

// questionLines is how much of a blocked screen is posted with a question.
const questionLines = 30

// PostBlocked reads a blocked agent's screen and posts the question with a
// button per option. The same screen is posted once per blocked episode;
// duplicate events and sweeps within that episode stay quiet.
func (d *Daemon) PostBlocked(ctx context.Context, a domain.Agent) {
	thread, ok := d.mapping.ThreadFor(a.PaneID)
	if !ok {
		return
	}
	screen, _, dialog, err := d.herdr.ReadForDialog(ctx, a.PaneID, a.Kind, 60)
	if err != nil {
		d.log.Warn("read blocked screen", slog.String("pane", a.PaneID), slog.String("err", err.Error()))
		return
	}
	if screen.Text == "" {
		return
	}
	sum := sha256.Sum256([]byte(screen.Text))
	hash := hex.EncodeToString(sum[:])
	if hash == d.lastPosted[a.PaneID] {
		return
	}
	// A second question supersedes the first; retire its buttons.
	d.retireKeyboard(ctx, a.PaneID)
	out := domain.Outgoing{
		ChatID:   d.chatID,
		ThreadID: thread,
		Text:     questionText(dialog, screen.Text),
		Notify:   d.shouldNotify(),
	}
	if dialog.Usable() {
		out.Buttons = choiceButtons(dialog)
	}
	id, err := d.tg.Send(ctx, out)
	if err != nil {
		d.log.Warn("post question", slog.String("pane", a.PaneID), slog.String("err", err.Error()))
		return
	}
	d.lastPosted[a.PaneID] = hash
	if dialog.Usable() {
		d.questions[a.PaneID] = &question{messageID: id, dialog: dialog}
		d.log.Info("question posted",
			slog.String("pane", a.PaneID), slog.Int("thread", thread),
			slog.Int("message", id), slog.Int("buttons", len(out.Buttons)))
	}
}

// PostDone posts a notification to the topic when an agent finishes its task.
func (d *Daemon) PostDone(ctx context.Context, a domain.Agent) {
	// One completion per done episode, even if a terminal clock or footer
	// changes between reconciliation sweeps. observeStatus resets this on
	// the next status transition.
	const postedDone = "done"
	if d.lastPosted[a.PaneID] == postedDone {
		return
	}
	thread, ok := d.mapping.ThreadFor(a.PaneID)
	if !ok {
		return
	}
	screen, err := d.herdr.ReadScreen(ctx, a.PaneID, domain.ScreenDetection, 25)
	if err != nil || screen.Text == "" {
		screen, _ = d.herdr.ReadScreen(ctx, a.PaneID, domain.ScreenRecentUnwrapped, 25)
	}
	name := a.DisplayName()
	text := fmt.Sprintf("🏆 任务已完成 · %s", name)
	if screen.Text != "" {
		clean := domain.TrimScreenChrome(screen.Text)
		if clean != "" {
			text += "\n\n" + tailLines(clean, 15)
		}
	}
	d.retireKeyboard(ctx, a.PaneID)
	out := domain.Outgoing{
		ChatID:   d.chatID,
		ThreadID: thread,
		Text:     text,
		Notify:   d.shouldNotify(),
	}
	if _, err := d.tg.Send(ctx, out); err != nil {
		d.log.Warn("post done notification", slog.String("pane", a.PaneID), slog.String("err", err.Error()))
		return
	}
	d.lastPosted[a.PaneID] = postedDone
	d.log.Info("done notification posted", slog.String("pane", a.PaneID), slog.Int("thread", thread))
}

// questionText formats what Telegram displays above the keyboard.
// It trims trailing terminal chrome and prefixes with ❓ without
// duplicating the title twice.
func questionText(dg domain.Dialog, screen string) string {
	clean := strings.TrimSpace(domain.TrimScreenChrome(screen))
	text := strings.TrimSpace(tailLines(clean, questionLines))
	if text == "" {
		text = dg.Title
	}
	if text == "" {
		text = "等待确认，请查看终端。"
	}
	return "❓ " + strings.TrimPrefix(text, "❓ ")
}

// tailLines keeps the last n lines of a screen.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// choiceButtons builds one button per option, plus the multi-select submit
// row and the free-text entry when the dialog has them. Callback data is
// an index into Choices (or a verb), well under Telegram's 64-byte cap.
func choiceButtons(dg domain.Dialog) []domain.Button {
	buttons := make([]domain.Button, 0, len(dg.Choices)+1)
	for i, c := range dg.Choices {
		label := c.Caption
		if label == "" {
			label = c.Label
		}
		buttons = append(buttons, domain.Button{
			Text: domain.CutLabel(label, 64),
			Data: strconv.Itoa(i + 1),
		})
	}
	if dg.Multi {
		buttons = append(buttons, domain.Button{Text: "✔ Submit", Data: domain.CallbackSubmit})
	}
	if dg.TextEntry > 0 {
		label := dg.TextLabel
		if label == "" {
			label = "type text"
		}
		buttons = append(buttons, domain.Button{
			Text: "✏️ " + domain.CutLabel(label, 24),
			Data: domain.CallbackTextEntryPrefix + strconv.Itoa(dg.TextEntry),
		})
	}
	return buttons
}

// handleButton answers a press on a question's keyboard.
func (d *Daemon) handleButton(ctx context.Context, b domain.ButtonPress) {
	var paneID string
	for pane, q := range d.questions {
		if q.messageID == b.MessageID {
			paneID = pane
			break
		}
	}
	if paneID == "" {
		// After a restart or once answered, the buttons are dead weight.
		_ = d.tg.AnswerCallback(ctx, b.CallbackID, "expired", false)
		_ = d.tg.EditKeyboard(ctx, b.ChatID, b.MessageID, nil)
		return
	}
	q := d.questions[paneID]
	if !d.refreshQuestion(ctx, paneID, q) {
		_ = d.tg.AnswerCallback(ctx, b.CallbackID, "the agent moved on", false)
		d.retireKeyboard(ctx, paneID)
		return
	}
	switch {
	case b.Data == domain.CallbackSubmit:
		if !q.dialog.Multi {
			_ = d.tg.AnswerCallback(ctx, b.CallbackID, "expired", false)
			return
		}
		if err := d.herdr.SendKeys(ctx, paneID, q.dialog.SubmitKeys()); err != nil {
			_ = d.tg.AnswerCallback(ctx, b.CallbackID, "⚠️ "+err.Error(), true)
			return
		}
		d.retireKeyboard(ctx, paneID)
		_ = d.tg.AnswerCallback(ctx, b.CallbackID, "submitted", false)
	case strings.HasPrefix(b.Data, domain.CallbackTextEntryPrefix):
		n, err := strconv.Atoi(strings.TrimPrefix(b.Data, domain.CallbackTextEntryPrefix))
		if err != nil || n < 1 || n > domain.MaxChoiceKeys || n != q.dialog.TextEntry {
			_ = d.tg.AnswerCallback(ctx, b.CallbackID, "expired", false)
			return
		}
		// The entry is opened with its digit; the free text follows as the
		// next message in the topic.
		if err := d.herdr.SendKeys(ctx, paneID, []string{strconv.Itoa(n)}); err != nil {
			_ = d.tg.AnswerCallback(ctx, b.CallbackID, "⚠️ "+err.Error(), true)
			return
		}
		d.retireKeyboard(ctx, paneID)
		d.typing[paneID] = typingWait{messageID: q.messageID, until: d.clock.Now().Add(typingWaitTTL)}
		_ = d.tg.AnswerCallback(ctx, b.CallbackID, "now send the text", false)
	default:
		i, err := strconv.Atoi(b.Data)
		if err != nil || i < 1 || i > len(q.dialog.Choices) {
			_ = d.tg.AnswerCallback(ctx, b.CallbackID, "expired", false)
			return
		}
		choice := q.dialog.Choices[i-1]
		if err := d.herdr.SendKeys(ctx, paneID, q.dialog.KeysFor(choice)); err != nil {
			_ = d.tg.AnswerCallback(ctx, b.CallbackID, "⚠️ "+err.Error(), true)
			return
		}
		d.log.Info("button answered", slog.String("pane", paneID), slog.String("data", b.Data), slog.Any("keys", q.dialog.KeysFor(choice)))
		if q.dialog.Multi {
			// A toggle leaves the dialog open: redraw from a fresh read.
			if refreshed := d.rereadDialog(ctx, paneID, q); refreshed != nil {
				_ = d.tg.EditKeyboard(ctx, d.chatID, q.messageID, choiceButtons(*refreshed))
				_ = d.tg.AnswerCallback(ctx, b.CallbackID, "toggled", false)
				return
			}
			d.retireKeyboard(ctx, paneID)
			_ = d.tg.AnswerCallback(ctx, b.CallbackID, "submitted", false)
			return
		}
		d.retireKeyboard(ctx, paneID)
		_ = d.tg.AnswerCallback(ctx, b.CallbackID, "sent: "+choice.Label, false)
	}
}

// refreshQuestion checks every callback against the current dialog, including
// Submit and text entry. Only cursor/checkbox state may have changed.
func (d *Daemon) refreshQuestion(ctx context.Context, paneID string, q *question) bool {
	a, ok := d.agents[paneID]
	if !ok {
		return false
	}
	_, _, fresh, err := d.herdr.ReadForDialog(ctx, paneID, a.Kind, 60)
	old := q.dialog
	if err != nil || !fresh.Usable() || fresh.Kind != old.Kind || fresh.Style != old.Style ||
		fresh.Title != old.Title || fresh.Multi != old.Multi || fresh.TextEntry != old.TextEntry ||
		fresh.SubmitRow != old.SubmitRow || len(fresh.Choices) != len(old.Choices) {
		return false
	}
	for i, c := range fresh.Choices {
		previous := old.Choices[i]
		if c.Number != previous.Number || c.Label != previous.Label || c.Key != previous.Key {
			return false
		}
	}
	q.dialog = fresh
	return true
}

// rereadDialog refreshes the stored question from the screen after a
// multi-select toggle, returning nil when the dialog is gone.
func (d *Daemon) rereadDialog(ctx context.Context, paneID string, q *question) *domain.Dialog {
	a, ok := d.agents[paneID]
	if !ok {
		return nil
	}
	_, _, dg, err := d.herdr.ReadForDialog(ctx, paneID, a.Kind, 60)
	if err != nil || !dg.Usable() {
		return nil
	}
	q.dialog = dg
	return &dg
}

// retireKeyboard removes a question's buttons and forgets it.
func (d *Daemon) retireKeyboard(ctx context.Context, paneID string) {
	q, ok := d.questions[paneID]
	if !ok {
		return
	}
	delete(d.questions, paneID)
	if err := d.tg.EditKeyboard(ctx, d.chatID, q.messageID, nil); err != nil {
		d.log.Warn("retire keyboard", slog.Int("message", q.messageID), slog.String("err", err.Error()))
	}
}
