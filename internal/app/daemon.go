package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
	"github.com/spancerxing/herdr-telegram-bridge/internal/ports"
	"github.com/spancerxing/herdr-telegram-bridge/internal/system"
)

// RealClock is the wall clock; tests inject their own.
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Daemon is the long-running half of the plugin: it mirrors Herdr's agents
// into forum topics and carries messages both ways. Everything it touches —
// mapping, questions, typing waits — lives on the Run goroutine, so there
// are no locks; Telegram updates arrive as closures on a channel.
type Daemon struct {
	herdr     ports.Herdr
	tg        ports.Telegram
	store     *MappingStore
	mapping   *Mapping
	chatID    int64
	agents    map[string]domain.Agent
	operators []domain.Operator
	clock     ports.Clock
	log       *slog.Logger

	updates       chan func(context.Context)
	questions     map[string]*question  // paneID -> open question
	typing        map[string]typingWait // paneID -> pending free-text answer
	lastPosted    map[string]string     // paneID -> blocked screen hash or "done", within one status episode
	dashboardText string                // last dashboard text sent in this process
	idleChecker   system.IdleChecker    // detects keyboard/mouse idle time for quiet mode
}

func NewDaemon(h ports.Herdr, tg ports.Telegram, store *MappingStore, chatID int64, ops []domain.Operator, clock ports.Clock, log *slog.Logger) *Daemon {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Daemon{
		herdr: h, tg: tg, store: store,
		mapping: store.Load(), chatID: chatID,
		agents: map[string]domain.Agent{}, operators: ops, clock: clock, log: log,
		idleChecker: system.DefaultIdleChecker(),
		updates:     make(chan func(context.Context), 64),
		questions:   map[string]*question{},
		typing:      map[string]typingWait{},
		lastPosted:  map[string]string{},
	}
}

// SetIdleChecker overrides the idle detection source (used in tests).
func (d *Daemon) SetIdleChecker(c system.IdleChecker) {
	d.idleChecker = c
}

// QuietThreshold is how long the machine must be untouched before the operator is considered away.
const QuietThreshold = 3 * time.Minute

// shouldNotify returns true if notifications should sound:
// When the operator is at the computer (idle < QuietThreshold), notifications stay quiet.
// When the operator is away (idle >= QuietThreshold or idle check unavailable), it rings.
func (d *Daemon) shouldNotify() bool {
	if d.idleChecker == nil {
		return true
	}
	idle, err := d.idleChecker.IdleFor(context.Background())
	if err != nil {
		return true
	}
	return idle >= QuietThreshold
}

// persist writes the mapping back to disk, tagging it with the chat.
func (d *Daemon) persist() {
	d.mapping.ChatID = d.chatID
	if err := d.store.Save(d.mapping); err != nil {
		d.log.Warn("save mapping", slog.String("err", err.Error()))
	}
}

// Run reconciles once, then serves Herdr events and Telegram updates until
// ctx ends. The subscription is rebuilt whenever the pane set changes or
// the connection drops, with ListAgents as the source of truth.
func (d *Daemon) Run(ctx context.Context) error {
	sweep := time.NewTicker(15 * time.Second)
	defer sweep.Stop()
	return d.run(ctx, sweep.C)
}

func (d *Daemon) run(ctx context.Context, sweep <-chan time.Time) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := d.reconcile(ctx); err != nil {
		return err
	}
	streamErr := make(chan error, 1)
	go func() { streamErr <- d.tg.Stream(ctx, d) }()

	resub := make(chan struct{}, 1)
	trigger := func() {
		select {
		case resub <- struct{}{}:
		default: // a rebuild is already pending
		}
	}
	trigger()

	var cancelSub context.CancelFunc
	defer func() {
		if cancelSub != nil {
			cancelSub()
		}
	}()
	var events <-chan domain.Event
	var retry <-chan time.Time
	var subscribed []string
	// ponytail: Herdr 0.9.1 does not deliver pane events for panes without
	// their own subscription (measured: create+close of an unlisted pane
	// produced nothing on the global pane.closed/agent_detected subs), so a
	// periodic sweep of agent.list is what discovers new panes — the same
	// net the plugin this replaces uses. Drop it if a server upgrade adds
	// true workspace-wide pane events.
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-streamErr:
			if err != nil {
				return fmt.Errorf("telegram updates stopped: %w", err)
			}
			return nil
		case <-sweep:
			if err := d.reconcile(ctx); err != nil {
				d.log.Warn("sweep reconcile", slog.String("err", err.Error()))
			} else if !samePaneSet(subscribed, d.agents) {
				trigger()
			}
		case fn, ok := <-d.updates:
			if ok {
				fn(ctx)
			}
		case ev, ok := <-events:
			if !ok {
				events = nil
				trigger()
				continue
			}
			d.handleEvent(ctx, ev)
			if ev.Kind == domain.EventAgentDetected || ev.Kind == domain.EventPaneClosed || ev.Kind == domain.EventResubscribed {
				// ponytail: rebuilding leaves a tiny event gap; use a
				// draining handoff if this daemon proves the gap matters.
				trigger()
			}
		case <-resub:
			live, err := d.refreshAgents(ctx)
			if err != nil {
				d.log.Warn("agent.list failed", slog.String("err", err.Error()))
				retry = time.After(5 * time.Second)
				continue
			}
			if cancelSub != nil {
				cancelSub()
			}
			subCtx, cancel := context.WithCancel(ctx)
			ch, err := d.herdr.Subscribe(subCtx, paneIDs(live))
			if err != nil {
				cancel()
				d.log.Warn("subscribe failed; retrying", slog.String("err", err.Error()))
				retry = time.After(5 * time.Second)
				continue
			}
			cancelSub, events = cancel, ch
			subscribed = paneIDs(live)
			retry = nil
		case <-retry:
			retry = nil
			trigger()
		}
	}
}

func samePaneSet(panes []string, agents map[string]domain.Agent) bool {
	if len(panes) != len(agents) {
		return false
	}
	for _, pane := range panes {
		if _, ok := agents[pane]; !ok {
			return false
		}
	}
	return true
}

func paneIDs(agents []domain.Agent) []string {
	panes := make([]string, 0, len(agents))
	for _, a := range agents {
		panes = append(panes, a.PaneID)
	}
	return panes
}

func (d *Daemon) refreshAgents(ctx context.Context) ([]domain.Agent, error) {
	live, err := d.herdr.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	next := make(map[string]domain.Agent, len(live))
	for _, a := range live {
		d.observeStatus(ctx, a)
		next[a.PaneID] = a
	}
	for pane := range d.agents {
		if _, exists := next[pane]; !exists {
			delete(d.lastPosted, pane)
			delete(d.questions, pane)
			delete(d.typing, pane)
		}
	}
	d.agents = next
	return live, nil
}

// A status transition starts a new notification episode, even when the
// terminal draws exactly the same question or result as the previous one.
// Both event delivery and reconciliation must observe these boundaries.
func (d *Daemon) observeStatus(ctx context.Context, a domain.Agent) {
	previous, known := d.agents[a.PaneID]
	if !known || previous.Status != a.Status {
		delete(d.lastPosted, a.PaneID)
	}
	if a.Status != domain.StatusBlocked {
		d.retireKeyboard(ctx, a.PaneID)
	}
}

// reconcile mirrors the live agents into topics: create what is missing,
// reopen what came back, refresh stale icons, close what died, and post
// notifications for agents already blocked or done.
func (d *Daemon) reconcile(ctx context.Context) error {
	live, err := d.refreshAgents(ctx)
	if err != nil {
		return fmt.Errorf("agent.list: %w", err)
	}
	liveSet := map[string]bool{}
	for _, a := range live {
		liveSet[a.PaneID] = true
		d.ensureTopic(ctx, a)
	}
	for _, pane := range d.mapping.Orphans(liveSet) {
		e := d.mapping.Topics[pane]
		if err := d.tg.DeleteTopic(ctx, d.chatID, e.ThreadID); err != nil {
			d.log.Warn("delete orphan topic", slog.String("pane", pane), slog.String("err", err.Error()))
			_ = d.tg.CloseTopic(ctx, d.chatID, e.ThreadID)
			d.mapping.MarkClosed(pane, true)
		} else {
			d.mapping.Remove(pane)
			d.log.Info("topic deleted", slog.String("pane", pane), slog.Int("thread", e.ThreadID))
		}
	}
	d.refreshDashboard(ctx)
	d.persist()
	for _, a := range live {
		switch a.Status {
		case domain.StatusBlocked:
			d.PostBlocked(ctx, a)
		case domain.StatusDone:
			d.PostDone(ctx, a)
		}
	}
	return nil
}

// ensureTopic gives a live agent an open topic with a current icon, and
// reports whether it has one now.
func (d *Daemon) ensureTopic(ctx context.Context, a domain.Agent) bool {
	name := a.DisplayName()
	e, ok := d.mapping.Topics[a.PaneID]
	if !ok {
		thread, err := d.tg.CreateTopic(ctx, d.chatID, name, d.tg.IconFor(a.Status))
		if err != nil {
			d.log.Warn("create topic", slog.String("pane", a.PaneID), slog.String("name", name), slog.String("err", err.Error()))
			return false
		}
		d.log.Info("topic created", slog.String("pane", a.PaneID), slog.String("name", name), slog.Int("thread", thread))
		d.mapping.Link(a.PaneID, &Entry{ThreadID: thread, Name: name, Status: a.Status})
		return true
	}
	if e.Closed {
		if err := d.tg.ReopenTopic(ctx, d.chatID, e.ThreadID); err != nil {
			d.log.Warn("reopen topic", slog.String("pane", a.PaneID), slog.Int("thread", e.ThreadID), slog.String("err", err.Error()))
			if isTopicGone(err) {
				delete(d.mapping.Topics, a.PaneID)
				d.persist()
			}
			return false
		}
		e.Closed = false
		e.Status = "" // reopened: refresh the icon below
	}
	nameChange := name != "" && name != e.Name
	statusChange := e.Status != a.Status
	if nameChange || statusChange {
		targetName := ""
		if nameChange {
			targetName = name
		}
		if err := d.tg.EditTopic(ctx, d.chatID, e.ThreadID, targetName, d.tg.IconFor(a.Status)); err != nil {
			d.log.Warn("edit topic", slog.String("pane", a.PaneID), slog.String("err", err.Error()))
			if isTopicGone(err) {
				delete(d.mapping.Topics, a.PaneID)
				d.persist()
			}
		} else {
			if nameChange {
				e.Name = name
			}
			e.Status = a.Status
		}
	}
	return true
}

func isTopicGone(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "topic gone") || strings.Contains(s, "topic_id_invalid") || strings.Contains(s, "topic_deleted") || strings.Contains(s, "thread not found")
}

func (d *Daemon) handleEvent(ctx context.Context, ev domain.Event) {
	switch ev.Kind {
	case domain.EventAgentStatusChanged:
		d.handleStatus(ctx, ev)
	case domain.EventAgentDetected:
		// A pane (re)acquired an agent; give it a topic if it has none.
		if _, err := d.refreshAgents(ctx); err != nil {
			d.log.Warn("agent.list failed", slog.String("err", err.Error()))
			return
		}
		if a, ok := d.agents[ev.PaneID]; ok {
			if d.ensureTopic(ctx, a) {
				d.refreshDashboard(ctx)
				d.persist()
			}
		}
	case domain.EventPaneClosed:
		d.handleClosed(ctx, ev.PaneID)
	case domain.EventResubscribed:
		// Events may have been missed; a full reconcile heals any drift.
		if err := d.reconcile(ctx); err != nil {
			d.log.Warn("reconcile", slog.String("err", err.Error()))
		}
	}
}

func (d *Daemon) handleStatus(ctx context.Context, ev domain.Event) {
	a, ok := d.agents[ev.PaneID]
	if !ok {
		// A pane this daemon has not seen; learn its kind before reading it.
		if _, err := d.refreshAgents(ctx); err != nil {
			d.log.Warn("agent.list failed", slog.String("err", err.Error()))
			return
		}
		a, ok = d.agents[ev.PaneID]
		if !ok {
			d.log.Debug("status for unknown pane", slog.String("pane", ev.PaneID))
			return
		}
	}
	a.Status = ev.Status
	d.observeStatus(ctx, a)
	d.agents[ev.PaneID] = a
	if e, ok := d.mapping.Topics[ev.PaneID]; ok && e.Status != ev.Status {
		if err := d.tg.EditTopic(ctx, d.chatID, e.ThreadID, "", d.tg.IconFor(ev.Status)); err != nil {
			d.log.Warn("topic icon", slog.String("pane", ev.PaneID), slog.String("err", err.Error()))
		} else {
			e.Status = ev.Status
			d.persist()
		}
	}
	d.refreshDashboard(ctx)
	if ev.Status == domain.StatusBlocked {
		d.PostBlocked(ctx, a)
		return
	}
	if ev.Status == domain.StatusDone {
		d.PostDone(ctx, a)
	}
	if _, open := d.questions[ev.PaneID]; open {
		d.retireKeyboard(ctx, ev.PaneID)
	}
}

func (d *Daemon) handleClosed(ctx context.Context, paneID string) {
	delete(d.agents, paneID)
	delete(d.lastPosted, paneID)
	delete(d.questions, paneID)
	delete(d.typing, paneID)
	e, ok := d.mapping.Topics[paneID]
	if !ok {
		d.refreshDashboard(ctx)
		d.persist()
		return
	}
	if err := d.tg.DeleteTopic(ctx, d.chatID, e.ThreadID); err != nil {
		d.log.Warn("delete topic", slog.String("pane", paneID), slog.String("err", err.Error()))
		_ = d.tg.CloseTopic(ctx, d.chatID, e.ThreadID)
		d.mapping.MarkClosed(paneID, true)
	} else {
		d.mapping.Remove(paneID)
		d.log.Info("topic deleted", slog.String("pane", paneID), slog.Int("thread", e.ThreadID))
	}
	d.refreshDashboard(ctx)
	d.persist()
}

// answerInTopic carries what an operator wrote in a topic to its agent.
func (d *Daemon) answerInTopic(ctx context.Context, m domain.TopicMessage) {
	paneID, ok := d.mapping.PaneForThread(m.ThreadID)
	if !ok {
		return
	}
	if m.Attachment != nil {
		d.reply(ctx, m.ThreadID, "📎 file attachments are not supported yet")
		return
	}
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	if text == "" || strings.HasPrefix(text, "/") {
		return
	}
	// An open ✏️ wait: this message is the free-text answer the button
	// asked for, and its retired keyboard needs nothing further.
	if w, waiting := d.typing[paneID]; waiting && d.clock.Now().Before(w.until) {
		delete(d.typing, paneID)
	}
	if err := d.herdr.Prompt(ctx, paneID, text); err != nil {
		d.reply(ctx, m.ThreadID, "⚠️ "+err.Error())
		return
	}
	d.log.Info("message forwarded", slog.String("pane", paneID), slog.Int("thread", m.ThreadID))
}

func (d *Daemon) reply(ctx context.Context, threadID int, text string) {
	if _, err := d.tg.Send(ctx, domain.Outgoing{ChatID: d.chatID, ThreadID: threadID, Text: text}); err != nil {
		d.log.Warn("reply", slog.Int("thread", threadID), slog.String("err", err.Error()))
	}
}

// The UpdateHandler methods run on the polling goroutine; each enqueues its
// work so all state stays on the Run goroutine. A full queue drops the
// update with a warning rather than blocking the poller.

func (d *Daemon) TopicMessage(_ context.Context, m domain.TopicMessage) {
	d.enqueue(func(ctx context.Context) { d.answerInTopic(ctx, m) })
}

func (d *Daemon) GeneralMessage(_ context.Context, m domain.TopicMessage) {
	d.enqueue(func(ctx context.Context) {
		d.log.Debug("message in General", slog.String("text", m.Text))
	})
}

func (d *Daemon) Button(_ context.Context, b domain.ButtonPress) {
	d.enqueue(func(ctx context.Context) { d.handleButton(ctx, b) })
}

// ponytail: hand renames are not mirrored back to agent.rename and a
// hand-closed topic is not treated as a mute; both land with the dashboard
// round.
func (d *Daemon) TopicEdited(_ context.Context, e domain.TopicEdit) {
	d.enqueue(func(ctx context.Context) {
		d.log.Debug("topic edited by hand",
			slog.Int("thread", e.ThreadID), slog.String("name", e.Name),
			slog.Bool("closed", e.Closed), slog.Bool("reopened", e.Reopened))
	})
}

func (d *Daemon) BotRightsChanged(_ context.Context, r domain.BotRights) {
	d.enqueue(func(ctx context.Context) {
		d.log.Info("bot rights changed",
			slog.String("status", r.Status),
			slog.Bool("topics", r.CanManageTopics),
			slog.Bool("delete", r.CanDeleteMessages),
			slog.Bool("pin", r.CanPinMessages))
	})
}

func (d *Daemon) enqueue(fn func(context.Context)) {
	select {
	case d.updates <- fn:
	default:
		d.log.Warn("telegram update dropped; daemon busy")
	}
}
