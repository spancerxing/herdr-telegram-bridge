package domain

import "strings"

// Kind is an agent kind as Herdr names it on the wire ("agent" field of
// agent.list, "kind" of agent.start). Only the four kinds this plugin
// carries a dialog profile for are listed; anything else parses with the
// generic profile, which still understands a plain numbered dialog.
type Kind string

const (
	KindClaude Kind = "claude"
	KindCodex  Kind = "codex"
	KindAgy    Kind = "agy"
	KindPi     Kind = "pi"
)

// SupportedKinds is the list the setup action offers and the order the
// docs use.
var SupportedKinds = []Kind{KindClaude, KindCodex, KindAgy, KindPi}

// APITarget names the value of the integration.install / integration.list
// `target` field for this kind.
//
// Note the underscore: Herdr's socket API spells Antigravity
// "antigravity_cli" while its CLI takes "antigravity-cli", and the label it
// reports is the hyphenated one. Verified against integration.list on Herdr
// 0.9.1: {target: "antigravity_cli", label: "antigravity-cli", command:
// "agy"}. Getting this wrong silently installs nothing.
func (k Kind) APITarget() string {
	if k == KindAgy {
		return "antigravity_cli"
	}
	return string(k)
}

// CLITarget is the spelling `herdr integration install <target>` accepts, for
// the docs and the setup instructions the operator reads.
func (k Kind) CLITarget() string {
	if k == KindAgy {
		return "antigravity-cli"
	}
	return string(k)
}

// Command is the executable Herdr looks for to decide whether this kind's
// integration is available on the machine. Antigravity's binary is `agy`,
// not the integration's name.
func (k Kind) Command() string {
	if k == KindAgy {
		return "agy"
	}
	return string(k)
}

// NeedsStateEmitter reports whether the kind needs this plugin's companion
// extension to feed it state. Only pi, and only because its hook is a
// consumer of the inter-extension event "herdr:blocked" that nothing emits.
//
// Measured against the hooks embedded in Herdr 0.9.1 (2026-09-20):
//
//	claude  session-only (SessionStart -> pane.report_agent_session)
//	codex   session-only
//	agy     session-only (antigravity-cli, integration version 3)
//	pi      reports state, but only when something emits the pi event
//	        "herdr:blocked"; pi itself never does, so a companion pi
//	        extension has to. See pi-extension/ in this repo.
//
// For the session-only kinds the state comes from Herdr's own detection
// manifest, which is what the plugin subscribes to.
func (k Kind) NeedsStateEmitter() bool {
	return k == KindPi
}

// ParseKind maps a Herdr agent name to a Kind case-insensitively.
// Aliases Herdr itself accepts are folded in; unknown kinds return "".
func ParseKind(s string) Kind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "claude", "claude-code":
		return KindClaude
	case "codex":
		return KindCodex
	case "agy", "antigravity", "antigravity-cli", "antigravity_cli":
		return KindAgy
	case "pi", "herdr:pi":
		return KindPi
	default:
		return ""
	}
}

// DisplayName is what Telegram topics and posts show for the kind.
func (k Kind) DisplayName() string {
	if k == "" {
		return "agent"
	}
	return string(k)
}

// IsProfiled reports whether this plugin carries measured screen rules for
// the kind. An unknown kind still works through the generic dialog parser and
// Herdr's own detection, it just has no kind-specific tweaks.
func (k Kind) IsProfiled() bool {
	for _, s := range SupportedKinds {
		if s == k {
			return true
		}
	}
	return false
}
