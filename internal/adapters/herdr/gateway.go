package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// agentStartMinTimeout and agentStartMaxTimeout mirror the bounds Herdr
// enforces on agent.start timeout_ms (documented as "greater than 3000 and
// at most 300000"). Sending a value outside them is rejected, so it is
// clamped rather than failed.
const (
	agentStartMinTimeout = 4 * time.Second
	agentStartMaxTimeout = 300 * time.Second
)

// reconnect backoff for the event subscription.
const (
	reconnectMin = 500 * time.Millisecond
	reconnectMax = 30 * time.Second
)

// Gateway implements ports.Herdr on top of Client.
type Gateway struct {
	client *Client
	log    *slog.Logger
	// eventBuf is the subscription channel's buffer. Deep enough to ride out
	// a burst of status flaps while the consumer posts to Telegram, which is
	// the slow part.
	eventBuf int
}

// NewGateway returns a gateway on a socket path.
func NewGateway(socketPath string, log *slog.Logger) *Gateway {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Gateway{client: NewClient(socketPath, log), log: log, eventBuf: 256}
}

// Path reports the socket in use.
func (g *Gateway) Path() string { return g.client.Path() }

// Ping reports the server version and protocol so the daemon can warn about
// a protocol it was not written for, and so the doctor can show them.
func (g *Gateway) Ping(ctx context.Context) (domain.HerdrInfo, error) {
	var res pongResult
	if err := g.client.call(ctx, "ping", nil, &res); err != nil {
		return domain.HerdrInfo{}, err
	}
	caps := res.Capabilities
	if caps == nil {
		caps = map[string]any{}
	}
	info := domain.HerdrInfo{Version: res.Version, Protocol: res.Protocol, Capabilities: caps}
	if res.Protocol != ProtocolVersion {
		g.log.Warn("herdr protocol differs from the one this build targets",
			slog.Int("got", res.Protocol),
			slog.Int("want", ProtocolVersion),
			slog.String("version", res.Version))
	}
	return info, nil
}

// ListAgents returns every agent Herdr knows about.
func (g *Gateway) ListAgents(ctx context.Context) ([]domain.Agent, error) {
	var res agentListResult
	if err := g.client.call(ctx, "agent.list", nil, &res); err != nil {
		return nil, err
	}
	out := make([]domain.Agent, 0, len(res.Agents))
	for _, a := range res.Agents {
		out = append(out, toDomainAgent(a))
	}
	return out, nil
}

// ListWorkspaces returns the workspaces a new agent can be started in,
// sorted the way Herdr numbers them so /new lists them in a stable order.
func (g *Gateway) ListWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	var res workspaceListResult
	if err := g.client.call(ctx, "workspace.list", nil, &res); err != nil {
		return nil, err
	}
	out := make([]domain.Workspace, 0, len(res.Workspaces))
	for _, w := range res.Workspaces {
		out = append(out, domain.Workspace{
			ID:      w.WorkspaceID,
			Label:   w.Label,
			Number:  w.Number,
			Focused: w.Focused,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// CreateTab opens an unfocused tab and returns its root pane. Focus is false
// deliberately: starting an agent from a phone must not steal the keyboard
// at the desk.
func (g *Gateway) CreateTab(ctx context.Context, workspaceID string) (domain.Tab, error) {
	var res tabCreatedResult
	if err := g.client.call(ctx, "tab.create", tabCreateParams{WorkspaceID: workspaceID, Focus: false}, &res); err != nil {
		return domain.Tab{}, err
	}
	return domain.Tab{
		ID:          res.Tab.TabID,
		WorkspaceID: res.Tab.WorkspaceID,
		Label:       res.Tab.Label,
		RootPaneID:  res.RootPane.PaneID,
	}, nil
}

// RenameTab sets a tab's label.
func (g *Gateway) RenameTab(ctx context.Context, tabID, label string) error {
	return g.client.call(ctx, "tab.rename", tabRenameParams{TabID: tabID, Label: label}, nil)
}

// ClosePane closes a pane through pane.close. A tab that held nothing else
// goes with it; a split tab keeps its other panes.
func (g *Gateway) ClosePane(ctx context.Context, paneID string) error {
	return g.client.call(ctx, "pane.close", paneCloseParams{PaneID: paneID}, nil)
}

// ReadScreen reads pane text.
//
// The source matters on protocol 22. "recent" is refused for a pane that is
// on the alternate screen and busy, with agent_not_idle, because that history
// can only be captured by scrolling while idle. ScreenRecentUnwrapped returns
// the same history without that restriction, so callers that want scrollback
// should ask for it and treat agent_not_idle as a signal to fall back to the
// visible screen rather than to retry.
func (g *Gateway) ReadScreen(ctx context.Context, paneID string, source domain.ScreenSource, lines int) (domain.Screen, error) {
	params := agentReadParams{
		Target:    paneID,
		Source:    string(source),
		Lines:     lines,
		Format:    "text",
		StripANSI: true,
	}
	var res agentReadResult
	if err := g.client.call(ctx, "agent.read", params, &res); err != nil {
		return domain.Screen{}, err
	}
	return domain.Screen{
		Text:      res.Read.Text,
		Revision:  res.Read.Revision,
		Truncated: res.Read.Truncated,
	}, nil
}

// Prompt types text into the agent and submits it.
//
// agent.prompt is tried first because it is atomic on the server: it types and
// submits in one request, so nothing can interleave. It refuses with
// agent_not_ready for an agent Herdr has launched but not finished
// registering, which on 0.9.1 is every agent started through agent.start:
// that call returns with launch_pending=true and a status of "unknown", and
// for pi the flag never clears at all because pi never reports
// interactive_ready. The pane is perfectly driveable in that state, so the
// refusal is handled by typing into the terminal and pressing enter, which the
// agent receives exactly as if the operator had typed it. Measured on this
// machine: agent.prompt refused a freshly started pi pane while
// pane.send_text + pane.send_keys worked.
//
// Only agent_not_ready is retried this way. A missing pane or a dead socket
// would fail identically on the second path.
func (g *Gateway) Prompt(ctx context.Context, paneID, text string) error {
	err := g.client.call(ctx, "agent.prompt", agentPromptParams{Target: paneID, Text: text}, nil)
	if err == nil || !isNotReady(err) {
		return err
	}
	g.log.Warn("agent.prompt refused the pane as not ready, falling back to pane input",
		slog.String("pane", paneID),
		slog.String("err", err.Error()))
	return g.TypeAndSubmit(ctx, paneID, text)
}

// TypeAndSubmit writes text into a pane's terminal and presses enter, without
// going through the agent-level readiness checks. This is the path used for
// agents Herdr considers still launching, and for any other caller that wants
// to reach the terminal rather than the agent abstraction.
func (g *Gateway) TypeAndSubmit(ctx context.Context, paneID, text string) error {
	if err := g.client.call(ctx, "pane.send_text", paneSendTextParams{PaneID: paneID, Text: text}, nil); err != nil {
		return err
	}
	return g.client.call(ctx, "pane.send_keys", paneSendKeysParams{PaneID: paneID, Keys: []string{domain.KeyEnter}}, nil)
}

// isNotReady reports whether the server refused because the agent has not
// finished registering, which the pane-level input path can bypass.
func isNotReady(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == CodeAgentNotReady
}

// SendKeys sends raw key names to the agent's terminal.
//
// Like agent.prompt, agent.send_keys is tried first and falls back to
// pane.send_keys when Herdr refuses with agent_not_ready (e.g. an agent still
// registering or launched without a persistent name).
func (g *Gateway) SendKeys(ctx context.Context, paneID string, keys []string) error {
	if keys == nil {
		keys = []string{}
	}
	err := g.client.call(ctx, "agent.send_keys", agentSendKeysParams{Target: paneID, Keys: keys}, nil)
	if err == nil || !isNotReady(err) {
		return err
	}
	g.log.Warn("agent.send_keys refused the pane as not ready, falling back to pane input",
		slog.String("pane", paneID),
		slog.String("err", err.Error()))
	return g.client.call(ctx, "pane.send_keys", paneSendKeysParams{PaneID: paneID, Keys: keys}, nil)
}

// Focus brings the agent's pane to the front.
func (g *Gateway) Focus(ctx context.Context, paneID string) error {
	return g.client.call(ctx, "agent.focus", agentFocusParams{Target: paneID}, nil)
}

// Rename sets the agent's custom name; nil clears it.
func (g *Gateway) Rename(ctx context.Context, paneID string, name *string) error {
	return g.client.call(ctx, "agent.rename", agentRenameParams{Target: paneID, Name: name}, nil)
}

// StartAgent starts an agent in an existing pane and waits until Herdr's
// detection has actually identified it.
//
// agent.start returns as soon as the process is spawned: measured on 0.9.1 it
// answered in 0.0s with agent_status "unknown", launch_pending true and an
// empty agent name, and its timeout_ms parameter does not make it wait despite
// the name. Detection catches up about four seconds later. Returning that
// first answer would hand callers an agent with no kind, so this polls
// agent.list until the pane carries an agent whose kind Herdr has resolved.
//
// Waiting on launch_pending would deadlock for pi, which never clears it, so
// readiness is judged by the kind and status alone.
func (g *Gateway) StartAgent(ctx context.Context, name, kind, paneID string, timeout time.Duration) (domain.Agent, error) {
	if timeout < agentStartMinTimeout {
		timeout = agentStartMinTimeout
	}
	if timeout > agentStartMaxTimeout {
		timeout = agentStartMaxTimeout
	}
	params := agentStartParams{
		Name:      name,
		Kind:      kind,
		PaneID:    paneID,
		TimeoutMS: timeout.Milliseconds(),
	}
	var res agentStartedResult
	if err := g.client.call(ctx, "agent.start", params, &res); err != nil {
		return domain.Agent{}, err
	}
	started := toDomainAgent(res.Agent)

	// The poll budget is separate from the call timeout so a slow detection is
	// not cut short by whichever is smaller.
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		agents, err := g.ListAgents(pollCtx)
		if err == nil {
			for _, a := range agents {
				if a.PaneID != paneID {
					continue
				}
				if a.Kind != "" && a.Status != domain.StatusUnknown {
					g.log.Info("agent detected",
						slog.String("pane", paneID),
						slog.String("kind", string(a.Kind)),
						slog.String("status", string(a.Status)))
					return a, nil
				}
			}
		} else if pollCtx.Err() != nil {
			return started, fmt.Errorf("agent.start %s: %w", paneID, pollCtx.Err())
		}
		select {
		case <-ticker.C:
		case <-pollCtx.Done():
			// Herdr may know the pane but not the agent: a kind that never
			// reports and never matches a manifest rule stays unknown.
			return started, fmt.Errorf("agent.start %s: no agent detected within %s", paneID, timeout)
		}
	}
}

// WaitStatus blocks until the pane reaches one of the wanted statuses or
// returns the server error, including a timeout.
func (g *Gateway) WaitStatus(ctx context.Context, paneID string, until []domain.Status, timeout time.Duration) (domain.Status, error) {
	names := make([]string, 0, len(until))
	for _, s := range until {
		names = append(names, string(s))
	}
	params := agentWaitParams{Target: paneID, Until: names, TimeoutMS: timeout.Milliseconds()}
	callCtx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()
	var res struct {
		AgentStatus string `json:"agent_status"`
	}
	err := g.client.callWith(callCtx, "agent.wait", params, &res)
	if err != nil {
		return domain.StatusUnknown, err
	}
	return domain.ParseStatus(res.AgentStatus), nil
}

// Notify raises a Herdr desktop notification, for telling the desk about
// something that happened in Telegram.
func (g *Gateway) Notify(ctx context.Context, title, body string, sound domain.NotifySound) error {
	params := notificationShowParams{Title: title, Body: body, Sound: string(sound)}
	if params.Sound == "" {
		params.Sound = string(domain.SoundNone)
	}
	return g.client.call(ctx, "notification.show", params, nil)
}

// IntegrationList reports which agent kinds have their state hook installed.
func (g *Gateway) IntegrationList(ctx context.Context) ([]domain.Integration, error) {
	var res integrationListResult
	if err := g.client.call(ctx, "integration.list", nil, &res); err != nil {
		return nil, err
	}
	out := make([]domain.Integration, 0, len(res.Integrations))
	for _, i := range res.Integrations {
		out = append(out, domain.Integration{
			Target:    i.Target,
			Label:     i.Label,
			Command:   i.Command,
			Available: i.Available,
			Path:      integrationPath(i.Target),
			Installed: i.State == "current",
			Outdated:  i.State == "outdated",
		})
	}
	return out, nil
}

// InstallIntegration writes a kind's bundled hook. This edits the agent CLI's
// own configuration directory, so the setup flow asks before calling it.
func (g *Gateway) InstallIntegration(ctx context.Context, target string) error {
	return g.client.call(ctx, "integration.install", integrationInstallParams{Target: target}, nil)
}

// BlockedReadOrder is the order in which screen sources are tried when
// looking for a dialog, best first.
//
// "detection" is first because it is the exact region Herdr's own manifests
// read, so it is the region the blocked status was decided from; "visible"
// is the fallback because it is the only source that never needs scrollback
// and therefore can never fail with agent_not_idle.
var BlockedReadOrder = []domain.ScreenSource{domain.ScreenDetection, domain.ScreenVisible}

// ScrollbackReadOrder is the order tried for reading history behind the
// visible screen. recent_unwrapped is preferred over recent because the
// wrapped variant is the one Herdr refuses with agent_not_idle while an
// alternate-screen pane is busy, which is the failure that made the previous
// generation of this plugin go blind on every full-screen TUI.
var ScrollbackReadOrder = []domain.ScreenSource{domain.ScreenRecentUnwrapped, domain.ScreenRecent, domain.ScreenVisible}

// ReadForDialog reads and parses the blocked pane, falling back when a source
// is readable but does not contain a usable dialog. When no source has a
// dialog, the first readable screen is returned for the plain-text fallback.
func (g *Gateway) ReadForDialog(ctx context.Context, paneID string, kind domain.Kind, lines int) (domain.Screen, domain.ScreenSource, domain.Dialog, error) {
	var first domain.Screen
	var firstSource domain.ScreenSource
	var lastErr error
	for _, src := range BlockedReadOrder {
		screen, err := g.ReadScreen(ctx, paneID, src, lines)
		if err != nil {
			lastErr = err
			if !isNotIdle(err) {
				if firstSource != "" {
					return first, firstSource, domain.Dialog{}, nil
				}
				return domain.Screen{}, src, domain.Dialog{}, err
			}
			continue
		}
		if firstSource == "" {
			first, firstSource = screen, src
		}
		if dialog := domain.ParseDialog(screen.Text, kind); dialog.Usable() {
			return screen, src, dialog, nil
		}
	}
	if firstSource != "" {
		return first, firstSource, domain.Dialog{}, nil
	}
	return domain.Screen{}, "", domain.Dialog{}, lastErr
}

// ReadScrollback reads history behind the visible screen, degrading to a
// smaller read rather than failing.
func (g *Gateway) ReadScrollback(ctx context.Context, paneID string, lines int) (domain.Screen, domain.ScreenSource, error) {
	return g.readFirst(ctx, paneID, ScrollbackReadOrder, lines)
}

// readFirst walks the sources and returns the first success. A refusal for a
// reason other than agent_not_idle is returned immediately: a missing pane
// or a dead socket will not be fixed by asking a different way.
func (g *Gateway) readFirst(ctx context.Context, paneID string, order []domain.ScreenSource, lines int) (domain.Screen, domain.ScreenSource, error) {
	var lastErr error
	for i, src := range order {
		screen, err := g.ReadScreen(ctx, paneID, src, lines)
		if err == nil {
			if i > 0 {
				g.log.Debug("screen read fell back",
					slog.String("pane", paneID),
					slog.String("source", string(src)),
					slog.Int("attempt", i+1),
					slog.String("first_err", errStringOf(lastErr)))
			}
			return screen, src, nil
		}
		lastErr = err
		if !isNotIdle(err) {
			return domain.Screen{}, src, err
		}
	}
	return domain.Screen{}, "", lastErr
}

// isNotIdle reports whether the server refused the read because the pane was
// busy on the alternate screen, which is the one refusal a different source
// can fix.
func isNotIdle(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == CodeAgentNotIdle
}

// IsAgentGone reports whether an error means the agent or pane no longer
// exists, in which case the topic should be closed rather than retried.
func IsAgentGone(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	return api.Code == CodeAgentNotFound || api.Code == CodePaneNotFound
}

func errStringOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Subscribe streams status and lifecycle events for the given panes.
//
// The channel is closed only when ctx ends. A dropped connection is retried
// with backoff and the subscriptions re-armed; after each successful
// re-subscription a domain.EventResubscribed is emitted so the consumer
// knows events may have been missed and re-reads agent.list instead of
// trusting its cache.
//
// Subscribing to pane ids directly (rather than to the workspace-wide
// pane.agent_status_changed) is what the protocol requires: that
// subscription's pane_id field is mandatory.
func (g *Gateway) Subscribe(ctx context.Context, paneIDs []string) (<-chan domain.Event, error) {
	subs := make([]subscription, 0, len(paneIDs)+2)
	for _, id := range paneIDs {
		subs = append(subs, subscription{Type: string(domain.EventAgentStatusChanged), PaneID: id})
	}
	subs = append(subs,
		subscription{Type: string(domain.EventAgentDetected)},
		subscription{Type: string(domain.EventPaneClosed)},
	)

	// Fail before starting the reconnect loop, and do not leak the probe socket.
	conn, err := g.client.dial(ctx)
	if err != nil {
		return nil, err
	}
	_ = conn.Close()

	ch := make(chan domain.Event, g.eventBuf)
	go g.runSubscription(ctx, subs, ch)
	return ch, nil
}

// runSubscription owns the reconnect loop for one Subscribe call.
func (g *Gateway) runSubscription(ctx context.Context, subs []subscription, ch chan<- domain.Event) {
	defer close(ch)
	backoff := reconnectMin
	started := false
	for {
		if ctx.Err() != nil {
			return
		}
		err := g.client.stream(ctx, subs, func() {
			if started {
				select {
				case ch <- domain.Event{Kind: domain.EventResubscribed, Received: time.Now()}:
				case <-ctx.Done():
				}
			}
			started = true
			backoff = reconnectMin
		}, func(env eventEnvelope) error {
			ev, ok := g.translate(env)
			if !ok {
				return nil
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// A rejected subscription is not worth retrying blind: log it
			// whole so a protocol change is visible rather than silent.
			var api *APIError
			if errors.As(err, &api) {
				g.log.Error("herdr subscription rejected", slog.String("code", api.Code), slog.String("err", api.Message))
			} else {
				g.log.Warn("herdr subscription ended", slog.String("err", err.Error()))
			}
		}

		g.log.Info("herdr subscription retry in", slog.Duration("backoff", backoff))
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > reconnectMax {
			backoff = reconnectMax
		}
	}
}

// translate maps one wire envelope to a domain event. The bool is false for
// events the plugin does not consume.
func (g *Gateway) translate(env eventEnvelope) (domain.Event, bool) {
	now := time.Now()
	switch domain.EventKind(env.Event) {
	case domain.EventAgentStatusChanged:
		var d statusChangedData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			g.log.Warn("status event not understood", slog.String("err", err.Error()))
			return domain.Event{}, false
		}
		return domain.Event{
			Kind:        domain.EventAgentStatusChanged,
			PaneID:      d.PaneID,
			WorkspaceID: d.WorkspaceID,
			AgentName:   deref(d.Agent),
			Status:      domain.ParseStatus(d.AgentStatus),
			HasStatus:   d.AgentStatus != "",
			Received:    now,
		}, true

	case domain.EventPaneClosed:
		var d paneClosedData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			g.log.Warn("pane.closed event not understood", slog.String("err", err.Error()))
			return domain.Event{}, false
		}
		return domain.Event{
			Kind:        domain.EventPaneClosed,
			PaneID:      d.PaneID,
			WorkspaceID: d.WorkspaceID,
			Status:      domain.StatusExited,
			HasStatus:   true,
			Received:    now,
		}, true

	case domain.EventAgentDetected:
		var d agentDetectedData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			g.log.Warn("agent_detected event not understood", slog.String("err", err.Error()))
			return domain.Event{}, false
		}
		ev := domain.Event{
			Kind:        domain.EventAgentDetected,
			PaneID:      d.PaneID,
			WorkspaceID: d.WorkspaceID,
			AgentName:   deref(d.Agent),
			Received:    now,
		}
		if d.FinalStatus != nil {
			ev.Status = domain.ParseStatus(*d.FinalStatus)
			ev.HasStatus = true
		}
		return ev, true

	default:
		return domain.Event{}, false
	}
}

// toDomainAgent maps the wire shape to the domain one. A JSON null in the
// nullable string fields arrives as "" and stays "".
func toDomainAgent(a agentInfo) domain.Agent {
	raw := a.Agent
	agent := domain.Agent{
		PaneID:                 a.PaneID,
		TerminalID:             a.TerminalID,
		RawKind:                raw,
		Kind:                   domain.ParseKind(raw),
		Name:                   deref(a.Name),
		DisplayAgent:           deref(a.DisplayAgent),
		Title:                  deref(a.Title),
		Status:                 domain.ParseStatus(a.AgentStatus),
		WorkspaceID:            a.WorkspaceID,
		TabID:                  a.TabID,
		Cwd:                    deref(a.Cwd),
		TerminalTitle:          deref(a.TerminalTitle),
		StateLabels:            a.StateLabels,
		InteractiveReady:       a.InteractiveReady,
		LaunchPending:          a.LaunchPending,
		ScreenDetectionSkipped: a.ScreenDetectionSkipped,
		Revision:               a.Revision,
		StateChangeSeq:         a.StateChangeSeq,
		Focused:                a.Focused,
	}
	if a.AgentSession != nil {
		switch a.AgentSession.Kind {
		case "id":
			agent.SessionID = a.AgentSession.Value
		case "path":
			agent.SessionPath = a.AgentSession.Value
		}
	}
	return agent
}

// integrationPath is where Herdr places each kind's hook on this machine.
// The paths are Herdr's own choice, not the plugin's; they are reported for
// the doctor and to tell the operator what installing will touch.
func integrationPath(target string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch target {
	case "claude":
		return home + "/.claude/hooks/herdr-agent-state.sh"
	case "codex":
		return home + "/.codex/herdr-agent-state.sh"
	case "antigravity_cli":
		return home + "/.gemini/config/hooks/herdr-agent-state.sh"
	case "pi":
		return home + "/.pi/agent/extensions/herdr-agent-state.ts"
	default:
		return ""
	}
}
