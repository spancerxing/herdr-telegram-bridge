package domain

// Status is the lifecycle state of an agent as Herdr reports it plus one
// plugin-internal value for a pane that went away.
//
// The five wire values are the AgentStatus enum of the Herdr socket API
// (protocol 22, schema_version 1, verified with `herdr api schema` on
// 2026-09-20): idle, working, blocked, done, unknown. StatusExited is
// never sent by Herdr — the plugin sets it when pane.closed arrives.
type Status string

const (
	StatusWorking Status = "working"
	StatusIdle    Status = "idle"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	StatusUnknown Status = "unknown"
	StatusExited  Status = "exited"
)

// ParseStatus maps a Herdr wire string to a Status. Anything outside the
// five values, including "exited", becomes StatusUnknown: Herdr never
// reports an exit as a status, so trusting that string would let a
// malformed payload close a topic.
func ParseStatus(s string) Status {
	switch Status(s) {
	case StatusWorking, StatusIdle, StatusBlocked, StatusDone, StatusUnknown:
		return Status(s)
	default:
		return StatusUnknown
	}
}

// Live reports whether the agent behind this status is still around.
func (s Status) Live() bool { return s != StatusExited }

// Emoji is the built-in glyph for the status and the default of the
// icons.<status> option. Runtime callers read Options.StatusIcons so an
// operator's choice wins; this is the seed.
func (s Status) Emoji() string {
	switch s {
	case StatusWorking:
		return "⚡"
	case StatusIdle:
		return "✅"
	case StatusBlocked:
		return "❓"
	case StatusDone:
		return "🏆"
	case StatusExited:
		return "🏁"
	default:
		return "👀"
	}
}

// Waiting reports whether the status means the agent needs the operator:
// the only status that rings and the only one that carries buttons.
func (s Status) Waiting() bool { return s == StatusBlocked }
