// Package herdr speaks the Herdr socket API (protocol 22, schema_version 1,
// Herdr 0.9.1). The wire format is newline-delimited JSON: one request per
// line, one response per line, and for a subscription many event lines on
// the same connection.
package herdr

import "encoding/json"

// ProtocolVersion is the socket protocol this adapter was written against.
//
// It is checked by the doctor and warned about at startup rather than
// enforced, because the plugin only needs a handful of methods and Herdr has
// so far kept them compatible. The previous generation of this plugin
// targeted protocol 17 and kept working against 22 with one real breakage
// (agent.read started refusing "recent" for a working alternate-screen
// pane), which is exactly the kind of thing this warning is for.
const ProtocolVersion = 22

// request is one call. Herdr requires params to be present, so it is
// serialised as {} rather than omitted.
type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

// response is one reply: exactly one of Result or Error is set.
type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *wireError      `json:"error"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// eventEnvelope is one subscription line. It carries no id, which is how a
// reader tells an event apart from a response.
type eventEnvelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// Error codes Herdr returns that the plugin reacts to by name.
const (
	// CodeAgentNotIdle is returned by agent.read when the pane is on the
	// alternate screen and busy. The fix is a different ReadSource, not a
	// retry.
	CodeAgentNotIdle = "agent_not_idle"
	// CodeTimeout is returned by agent.wait when the status did not arrive.
	CodeTimeout = "timeout"
	// CodeAgentNotFound and CodePaneNotFound mean the agent is gone.
	CodeAgentNotFound = "agent_not_found"
	CodePaneNotFound  = "pane_not_found"
	// CodeAgentNotReady is returned by agent.prompt for an agent Herdr has
	// launched but not finished registering. Measured on 0.9.1: every agent
	// started through agent.start carries launch_pending=true, and for pi that
	// flag never clears because pi never reports interactive_ready, so
	// agent.prompt refuses forever while the pane is perfectly driveable. The
	// pane-level input methods have no such gate and are the fallback.
	CodeAgentNotReady = "agent_not_ready"
)

// --- params -------------------------------------------------------------

type pingParams struct{}

type agentListParams struct{}

type agentReadParams struct {
	Target    string `json:"target"`
	Source    string `json:"source"`
	Lines     int    `json:"lines,omitempty"`
	Format    string `json:"format,omitempty"`
	StripANSI bool   `json:"strip_ansi"`
}

type agentPromptParams struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

type agentSendKeysParams struct {
	Target string   `json:"target"`
	Keys   []string `json:"keys"`
}

type agentFocusParams struct {
	Target string `json:"target"`
}

type agentRenameParams struct {
	Target string  `json:"target"`
	Name   *string `json:"name"`
}

type agentStartParams struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	PaneID    string   `json:"pane_id"`
	Args      []string `json:"args,omitempty"`
	TimeoutMS int64    `json:"timeout_ms,omitempty"`
}

type agentWaitParams struct {
	Target    string   `json:"target"`
	Until     []string `json:"until"`
	TimeoutMS int64    `json:"timeout_ms,omitempty"`
}

type tabCreateParams struct {
	WorkspaceID string `json:"workspace_id"`
	Focus       bool   `json:"focus"`
}

type tabRenameParams struct {
	TabID string `json:"tab_id"`
	Label string `json:"label"`
}

type paneCloseParams struct {
	PaneID string `json:"pane_id"`
}

// paneSendTextParams types text into a pane's terminal without a newline.
type paneSendTextParams struct {
	PaneID string `json:"pane_id"`
	Text   string `json:"text"`
}

// paneSendKeysParams sends named keys to a pane's terminal.
type paneSendKeysParams struct {
	PaneID string   `json:"pane_id"`
	Keys   []string `json:"keys"`
}

type workspaceListParams struct{}

type notificationShowParams struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Sound string `json:"sound,omitempty"`
}

type integrationListParams struct{}

type integrationInstallParams struct {
	Target string `json:"target"`
}

// subscription is one entry of an events.subscribe request. pane_id is
// required for the per-pane subscriptions and must be absent from the
// workspace-wide ones, so it is a pointer.
type subscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

type eventsSubscribeParams struct {
	Subscriptions []subscription `json:"subscriptions"`
}

// --- results ------------------------------------------------------------

// agentInfo is one entry of agent.list. Field names track the protocol
// exactly; the domain mapping happens in gateway.go.
type agentInfo struct {
	Agent                  string            `json:"agent"`
	AgentStatus            string            `json:"agent_status"`
	Cwd                    *string           `json:"cwd"`
	DisplayAgent           *string           `json:"display_agent"`
	Focused                bool              `json:"focused"`
	ForegroundCwd          *string           `json:"foreground_cwd"`
	InteractiveReady       bool              `json:"interactive_ready"`
	LaunchPending          bool              `json:"launch_pending"`
	Name                   *string           `json:"name"`
	PaneID                 string            `json:"pane_id"`
	Revision               int64             `json:"revision"`
	ScreenDetectionSkipped bool              `json:"screen_detection_skipped"`
	StateChangeSeq         int64             `json:"state_change_seq"`
	StateLabels            map[string]string `json:"state_labels"`
	TabID                  string            `json:"tab_id"`
	TerminalID             string            `json:"terminal_id"`
	TerminalTitle          *string           `json:"terminal_title"`
	Title                  *string           `json:"title"`
	WorkspaceID            string            `json:"workspace_id"`
	AgentSession           *agentSession     `json:"agent_session"`
}

type agentSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

type agentListResult struct {
	Agents []agentInfo `json:"agents"`
}

type agentReadResult struct {
	Read struct {
		Text      string `json:"text"`
		Revision  int64  `json:"revision"`
		Truncated bool   `json:"truncated"`
	} `json:"read"`
}

type agentStartedResult struct {
	Agent agentInfo `json:"agent"`
}

type tabCreatedResult struct {
	Tab struct {
		TabID       string `json:"tab_id"`
		WorkspaceID string `json:"workspace_id"`
		Label       string `json:"label"`
	} `json:"tab"`
	RootPane struct {
		PaneID string `json:"pane_id"`
	} `json:"root_pane"`
}

type workspaceListResult struct {
	Workspaces []struct {
		WorkspaceID string `json:"workspace_id"`
		Label       string `json:"label"`
		Number      int    `json:"number"`
		Focused     bool   `json:"focused"`
	} `json:"workspaces"`
}

type pongResult struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	// Not all values are booleans: endpoint_protocol_generation is a number
	// on Herdr 0.9.1, so this stays untyped and domain.HerdrInfo.Supports
	// decides what counts as on.
	Capabilities map[string]any `json:"capabilities"`
}

type integrationListResult struct {
	Integrations []struct {
		Target    string `json:"target"`
		Label     string `json:"label"`
		Command   string `json:"command"`
		Available bool   `json:"available"`
		State     string `json:"state"`
	} `json:"integrations"`
}

type subscriptionStartedResult struct {
	Type string `json:"type"`
}

// --- event payloads -----------------------------------------------------

type statusChangedData struct {
	PaneID       string            `json:"pane_id"`
	WorkspaceID  string            `json:"workspace_id"`
	Agent        *string           `json:"agent"`
	AgentStatus  string            `json:"agent_status"`
	DisplayAgent *string           `json:"display_agent"`
	StateLabels  map[string]string `json:"state_labels"`
	Title        *string           `json:"title"`
}

type agentDetectedData struct {
	PaneID      string  `json:"pane_id"`
	WorkspaceID string  `json:"workspace_id"`
	Agent       *string `json:"agent"`
	FinalStatus *string `json:"final_status"`
	Released    bool    `json:"released"`
}

type paneClosedData struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
}

// deref returns the value behind a nullable wire string.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
