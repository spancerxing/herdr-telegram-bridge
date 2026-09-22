package domain

import "time"

// Agent is one agent as agent.list reports it.
type Agent struct {
	// PaneID is the stable identity the plugin keys everything on:
	// mapping.json, the topic, the Telegram thread. Herdr keeps it for the
	// life of the pane, unlike the terminal id or the displayed name.
	PaneID string
	// TerminalID changes when a pane's terminal is replaced; it is kept for
	// logging and for matching a resume.
	TerminalID string
	// Kind is the agent executable's name as Herdr reports it (the "agent"
	// field), not yet folded through ParseKind.
	RawKind string
	Kind    Kind
	// Name is the agent's custom name, empty when none is set.
	Name string
	// DisplayAgent is Herdr's own display name for the agent, which is what
	// its UI shows. Preferred over the terminal title when present.
	DisplayAgent string
	// Title is the agent-supplied title, when the kind reports one.
	Title       string
	Status      Status
	WorkspaceID string
	TabID       string
	Cwd         string
	// TerminalTitle is the title the agent set; herdr's own UI shows a
	// cleaned form of it.
	TerminalTitle string
	// StateLabels maps a status to a human label Herdr attached to it, and
	// Herdr fills it from its own detection manifests. A status pushed by a
	// self-reporting agent through pane.report_agent does not contribute:
	// measured on 0.9.1, a blocked pi pane came back with state_labels empty
	// while its reporter had sent "confirm: Herdr bridge self-test". So the
	// label is not why a self-reporting kind skips screen scraping — it is
	// exactly the kinds Herdr detects that get one, and Blocked is the one
	// that matters, because it is the question summary.
	StateLabels map[string]string
	// SessionID and SessionPath locate the agent's own transcript, when the
	// kind's integration hook reported one. Used for done posts.
	SessionID   string
	SessionPath string
	// InteractiveReady is false while the agent is still starting up, and
	// LaunchPending while Herdr is waiting for it. Both gate posting.
	InteractiveReady bool
	LaunchPending    bool
	// ScreenDetectionSkipped records that Herdr itself declined to score the
	// screen for this kind (a manifest with skip_state_update), which means
	// the status came from a self-report rather than a scrape.
	ScreenDetectionSkipped bool
	// Revision and StateChangeSeq order status changes; the higher the
	// newer. StateChangeSeq is what the plugin uses to drop out-of-order
	// stream events.
	Revision       int64
	StateChangeSeq int64
	Focused        bool
}

// Label returns the label attached to a status by Herdr's detection, if there
// is one. Empty when there is none.
func (a Agent) Label(s Status) string {
	if a.StateLabels == nil {
		return ""
	}
	return a.StateLabels[string(s)]
}

// BlockedReason is the question summary Herdr's detection attached to the
// blocked status. Empty for every kind that reports its state rather than
// being detected, in which case the plugin reads the screen instead.
func (a Agent) BlockedReason() string { return a.Label(StatusBlocked) }

// DisplayName is the label a topic and the dashboard show: prefer the
// terminal title (which Herdr's UI displays), then Herdr's display name,
// then the agent's custom name, then the kind.
func (a Agent) DisplayName() string {
	for _, s := range []string{a.TerminalTitle, a.DisplayAgent, a.Title, a.Name} {
		if s != "" {
			return s
		}
	}
	return a.Kind.DisplayName()
}

// Workspace is one workspace as workspace.list reports it.
type Workspace struct {
	ID      string
	Label   string
	Number  int
	Focused bool
}

// Tab is one tab as tab.create returns it, with its root pane.
type Tab struct {
	ID          string
	WorkspaceID string
	Label       string
	RootPaneID  string
}

// Screen is text read out of a pane.
type Screen struct {
	Text      string
	Revision  int64
	Truncated bool
}

// ScreenSource selects which text agent.read returns. The enum is Herdr's
// ReadSource (protocol 22).
type ScreenSource string

const (
	// ScreenVisible is the text on screen right now.
	ScreenVisible ScreenSource = "visible"
	// ScreenRecent is the recent scrollback, wrapped as displayed. Herdr
	// refuses this for a pane on the alternate screen while it is working.
	ScreenRecent ScreenSource = "recent"
	// ScreenRecentUnwrapped is the recent scrollback with wrapping undone.
	// Added in protocol 22; this is the source that keeps working for
	// full-screen TUIs the way ScreenRecent did on protocol 17.
	ScreenRecentUnwrapped ScreenSource = "recent_unwrapped"
	// ScreenDetection is the region Herdr's own agent detection reads, which
	// is the most reliable place to look for a blocked dialog.
	ScreenDetection ScreenSource = "detection"
)

// NotifySound is the sound of a Herdr desktop notification.
type NotifySound string

const (
	SoundNone    NotifySound = "none"
	SoundDone    NotifySound = "done"
	SoundRequest NotifySound = "request"
)

// Integration is one agent integration as integration.list reports it.
type Integration struct {
	// Target is the socket API's name for the integration ("antigravity_cli").
	Target string
	// Label is Herdr's display spelling ("antigravity-cli").
	Label string
	// Command is the executable Herdr looks for.
	Command string
	// Available is false when the CLI is not installed, in which case there
	// is nothing to wire up.
	Available bool
	// Path is where the hook file lives for this machine.
	Path string
	// Installed reports whether the hook is in place and current.
	Installed bool
	// Outdated reports an installed hook older than the bundled one.
	Outdated bool
}

// HerdrInfo is the result of a ping.
type HerdrInfo struct {
	Version  string
	Protocol int
	// Capabilities the server advertises. Values are not all booleans: on
	// Herdr 0.9.1 "surface_interest" and "health_check" are true while
	// "endpoint_protocol_generation" is the number 1, so the map is
	// untyped and Supports decides what counts as on.
	Capabilities map[string]any
}

// Supports reports whether the server advertised a capability as on: true,
// a non-zero number, or a non-empty string. Used to fail loudly when a
// required feature is missing instead of silently misbehaving.
func (h HerdrInfo) Supports(name string) bool {
	v, ok := h.Capabilities[name]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	case nil:
		return false
	default:
		return true
	}
}

// EventKind names the subscription events the plugin consumes.
type EventKind string

const (
	// EventAgentStatusChanged is pane.agent_status_changed: the agent in a
	// pane entered a new status. This is the primary trigger for everything
	// the plugin posts.
	EventAgentStatusChanged EventKind = "pane.agent_status_changed"
	// EventAgentDetected is pane.agent_detected: an agent appeared in a pane.
	EventAgentDetected EventKind = "pane.agent_detected"
	// EventPaneClosed is pane.closed: the pane went away, so its agent exited.
	EventPaneClosed EventKind = "pane.closed"
	// EventPaneUpdated is pane.updated: cwd, title or scroll changed.
	EventPaneUpdated EventKind = "pane.updated"
	// EventResubscribed is emitted by the plugin itself, never by Herdr: the
	// subscription was re-established after a gap, so events may have been
	// missed and the consumer must re-read agent.list rather than trust its
	// cache.
	EventResubscribed EventKind = "plugin.resubscribed"
)

// Event is one subscription event, normalised across kinds.
type Event struct {
	Kind        EventKind
	PaneID      string
	WorkspaceID string
	// KindName is the agent name from an agent_detected event ("claude").
	AgentName string
	// Status is set on EventAgentStatusChanged. A nil AgentStatus on the
	// wire means the pane no longer holds an agent.
	Status    Status
	HasStatus bool
	// Seq orders events for one pane; higher is newer.
	Seq int64
	// Received is when the plugin saw it, for staleness checks against a
	// status read straight from agent.list.
	Received time.Time
}
