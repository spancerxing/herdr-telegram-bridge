# Design

This plugin drives Herdr agents from Telegram, across four agent kinds:
Claude Code, Codex, agy (Antigravity CLI) and pi. It replaces
`permgps/herdr-telegram-agents`, which worked but could not show buttons on
this machine.

Everything below was measured against the running Herdr on this machine
(0.9.1, socket protocol 22) on 2026-09-20, not read off documentation.
The commands that produced each finding are given so it can be re-checked
after a Herdr upgrade.

## Why the previous plugin showed no buttons

Buttons were never broken. Their single trigger never fired.

In the old plugin, buttons are attached in exactly one place:

```go
// internal/app/outbound.go:613, permgps/herdr-telegram-agents v0.10.0
if agent.Status == domain.StatusBlocked {
    dialog = domain.ParseDialog(text)
    out.Buttons = choiceButtons(dialog)
}
```

`StatusBlocked` is the only switch. Its daemon log over ~22 hours held:

| what | count |
|---|---|
| `"status":"done"` posts | 53 |
| `"status":"idle"` posts | 3 |
| `"status":"unknown"` posts | 6 |
| `"status":"blocked"` posts | **0** |
| `pager sent` | **0** |
| posts with `buttons > 0` | **0 of 48** |

So no agent ever reached `blocked`. Three separate causes stacked up.

### Cause 1: pi cannot report blocked at all

`~/.local/state/herdr/agent-detection/remote/pi.toml` (version 2026.09.14.1)
declares two rules, both `working`:

```
id = "working_literal"   contains = ["Working..."]
id = "working_border"    line_regex = ['^── [⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] Working ─+$']
```

There is no `blocked` rule. Herdr's screen detection therefore can never put
a pi pane into `blocked`, no matter what pi draws. Two of the three agents on
this machine were pi.

`agy.toml` has exactly one blocked rule (`permission_prompt`, matching
`"requesting permission for:"`), which is narrow. `claude.toml` (6.7 kB) and
`codex.toml` (3.2 kB) are rich.

Check this per kind with:

```bash
grep -c 'state = "blocked"' ~/.local/state/herdr/agent-detection/remote/<kind>.toml
herdr agent explain <pane> --json     # shows which rule matched
```

### Cause 2: the plugin asked for scrollback it was not allowed to have

The old capture loop used `source=recent` with `lines=400`. On protocol 22
Herdr refuses that for a pane on the alternate screen while it is busy.
911 of these were logged, 911 of them on the pi pane:

```
herdr agent.read: herdr api agent_not_idle: cannot read 400 lines while
w5Z:p1 is working: its alternate-screen history can only be captured by
scrolling while idle. Wait and retry, or use --source visible
```

Every full-screen TUI (pi, agy) uses the alternate screen, so the old plugin
was blind on exactly the kinds that needed help.

Measured on 0.9.1: with `lines=40` all four sources answer, `truncated=true`,
returning the visible screen without needing scrollback. Protocol 22 also
added `recent_unwrapped`, which is the source that does not carry the
restriction.

### Cause 3: the private-chat message never has buttons

Only the topic post gets an inline keyboard. The pager message that actually
rings is built without one:

```go
// internal/app/outbound.go:673
out := domain.Outgoing{Text: pagerText(...), HTML: true, Notify: true}
//                                                        ^ no Buttons
```

and the topic post is silenced when the pager is on (`out.Notify = false`).
So the one message a phone shows first is always plain text with a link. That
part is by design, not a bug, but it is what makes the plugin *look*
notification-only.

## What protocol 22 gives us instead

`herdr api schema --json` prints the whole contract (276 kB, schema_version 1).
The parts that change the architecture:

| capability | why it matters |
|---|---|
| `pane.agent_status_changed` subscription | Replaces polling. The server pushes status; the plugin reacts. |
| `agent.wait {until:[blocked]}` | The server resolves "wait for blocked", so no poll loop. |
| `ReadSource`: `visible`, `recent`, `recent_unwrapped`, `detection` | `recent_unwrapped` is the scrollback read that does not need an idle pane. `detection` is the exact region the blocked decision came from. |
| `AgentInfo.state_labels` | Herdr detection manifests can attach a human label. Self-reported `message` does not survive into this field on 0.9.1, so blocked content still comes from the screen. |
| `pane.report_agent {state, message}` | An agent can push its own state, including `blocked`; the bridge treats it as a trigger, not a source of dialog text. This is how pi can be fixed. |
| `integration.list` / `integration.install` | Herdr ships the per-kind hooks itself. |
| `AgentInfo.display_agent`, `title`, `agent_session` | Display name and session transcript path come from Herdr. |

Confirmed live: `events.subscribe` acks with `{"result":{"type":"subscription_started"}}`
and then streams `{"event":"pane.agent_status_changed","data":{...}}` on the
same long-lived connection. Subscriptions to `pane.agent_status_changed`
**require** a `pane_id`; `pane.agent_detected` and `pane.closed` are
workspace-wide.

## The per-kind matrix

This is the finding that shaped the design, and the one that is easy to get
wrong. The hooks Herdr installs are **not** all state reporters. Their source
is embedded in the Herdr binary; `herdr integration status` prints the paths.

| kind | hook | what it actually reports | where `blocked` comes from |
|---|---|---|---|
| claude | `~/.claude/hooks/herdr-agent-state.sh` (v10) | `SessionStart` only → `pane.report_agent_session` with `session_id`, `transcript_path` | Herdr detection manifest |
| codex | `~/.codex/herdr-agent-state.sh` (v8) | session identity only | Herdr detection manifest |
| agy | `~/.gemini/config/hooks/herdr-agent-state.sh` (v3) | session identity only | Herdr detection manifest (one narrow rule) |
| pi | `~/.pi/agent/extensions/herdr-agent-state.ts` | `pane.report_agent` with `state` and `message` — **but only when something emits `herdr:blocked`** | needs the companion extension in `pi-extension/` |

The claude hook is explicit about it:

```sh
case "$action" in
  session) ;;
  *) exit 0 ;;
esac
...
if hook_event_name != "SessionStart": raise SystemExit(0)
request = {"method": "pane.report_agent_session", ...}   # no state
```

The pi extension is a **consumer**, not a producer:

```ts
pi.events.on("herdr:blocked", (data) => {
  blockedCount += 1; blockedMessage = data.label; publishState();
});
```

`pi.events` is pi's inter-extension event bus (`docs/extensions.md:1768`,
"Inter-extension events"). Nothing in `@earendil-works/pi-coding-agent`
mentions herdr (`grep -r herdr` → 0 hits), so nothing emits `herdr:blocked`.
Installing `herdr integration install pi` alone therefore changes nothing.
Closing that gap is why this repo carries a pi extension.

So the "self-report first, screen fallback" strategy resolves to:

- **claude, codex** — nothing to do. Herdr's manifests are strong; subscribe
  to the status and read the screen only to extract option labels.
- **agy** — works, but from a single rule. Watch it; a local manifest override
  is the lever if it proves too narrow.
- **pi** — the companion extension must emit `herdr:blocked`, then Herdr's
  hook forwards it as a real `blocked` status. The bridge reads the screen for
  the title and options because the report message is not exposed.

### Two naming traps

1. The socket API spells Antigravity `antigravity_cli`; the CLI takes
   `antigravity-cli`; the binary is `agy`. `integration.list` returns
   `{target:"antigravity_cli", label:"antigravity-cli", command:"agy"}`.
   Using the hyphenated form against the socket silently installs nothing.
2. `pong.capabilities` is not a map of booleans. On 0.9.1
   `endpoint_protocol_generation` is the number `1`. Decoding it as
   `map[string]bool` fails the whole ping.

## Architecture

```
cmd/herdr-tg            CLI: probe, integrations, setup, startup, daemon
internal/domain         Kind, Status, Dialog parser, Telegram types
internal/ports          Herdr + Telegram interfaces the app depends on
internal/adapters/herdr protocol 22 client, gateway, screen read fallback
internal/adapters/telegram  Telegram Bot API, topics, messages, buttons
internal/app            reconciler, blocked flow, dashboard, notifications
pi-extension            the herdr:blocked emitter pi needs
```

Layering follows the plugin this replaces: `domain` has no imports outside
the standard library, `ports` names what the app needs, adapters implement
ports, and the app never imports an adapter. That is what makes the dialog
parser testable with no Herdr and no Telegram.

### Dialog parsing

`domain.ParseDialog(screen, kind)` is the piece the old plugin got wrong by
assuming Claude Code everywhere. It is a shared reader with a per-kind
`Profile`, and it recognises three shapes:

| style | shape | answer |
|---|---|---|
| numbered | `1. Yes / 2. No` | send the digit |
| yes/no | `[y/n]`, `allow command?` | send `y` / `n` |
| cursor | `→ Yes` / `  No` + `↑↓ navigate enter select` footer | arrow walk then enter |

Per kind:

| kind | cursor glyph | style | notes |
|---|---|---|---|
| claude | `❯` | numbered | `Type something` / `Chat about this` are service entries: dropped from buttons, numbers kept |
| codex | `›` | numbered, yes/no | trust-directory prompt, `[y/n]` |
| agy | `❯ › >` | numbered | |
| pi | `→` | **cursor** | pi numbers nothing and marks the selection with `→` |

**pi's dialog is not a Claude Code dialog at all.** Measured on a real blocked
pane (w5Z:p9, 2026-09-20):

```
 Herdr bridge self-test
 Continue with the bridge test?

 → Yes
   No

 ↑↓ navigate  enter select  escape/ctrl+c cancel
```

There is no `1.` anywhere, so the numbered reader finds nothing and the pane
would post with no buttons. The cursor style was found by driving the real
path, not by reading documentation.

Two details in the cursor reader are load-bearing:

- **The footer is required.** `navigate` *and* `select` must both appear in the
  hint line. Without that anchor, any two indented lines on screen would grow
  buttons.
- **Option indentation is relative to the cursor line, not absolute.** pi draws
  the dialog one column in, with unselected options two columns further. An
  absolute `indent >= 2` rule breaks the moment a pane renders the dialog at a
  deeper base column — the question line then reads as an option. The base
  column is taken from the cursor line, which is the one line whose role is
  certain, and options are collected outwards from it so a cursor resting in the
  middle of a list works too.

### `state_labels` is empty for self-reported status

Worth knowing, because it changes where the question text comes from. Herdr
fills `AgentInfo.state_labels` from its own detection **manifests**, not from
the `message` a self-reporting agent sends through `pane.report_agent`.
Measured: a blocked pi pane reported

```
status=blocked  state_labels={}  title=""  display=""  blocked reason=""
```

while the reporter had sent `"confirm: Herdr bridge self-test"`. The message is
dropped entirely — not just from `state_labels` but from `title` too. So the
question still has to come off the screen, and `Dialog.Title` carries it — for
that pane, `"Herdr bridge self-test · Continue with the bridge test?"`.

The lesson, stated once because it is the crux of the whole design: **the
self-report channel carries a status and nothing else.** `PaneReportAgentParams`
is `state` (four enum values) plus `message` (one nullable string), and the
message does not survive. A `blocked` status is a trigger to go and look, not a
description of what is being asked.

### Why options cannot come from the self-report

`pane.report_agent` has no field for a list of choices — searching the whole
276 kB protocol schema for `options`, `choices`, `dialog` or `buttons` returns
zero hits. So even if the reporter knew the options there is no channel to carry
them to the plugin.

The reporter does not know them either. pi's `ui_prompt_start` is
`{ type, reason, kind, title? }`: the options are an argument to the call that
opens the prompt (`ctx.ui.select(title, options)`), made by core pi or by
whatever other extension asked the question. This extension only *observes*
that a prompt opened; there is no API to inspect a pending one. Same for
`confirm`, where the two options are implicit in the call rather than spelled
out.

So for a `select` prompt the options exist in exactly one place the plugin can
reach: the screen.

### Two more protocol traps

- **`agent.start` does not wait.** Despite the name of its `timeout_ms`
  parameter, on 0.9.1 it answered in 0.0s with `agent_status: "unknown"`,
  `launch_pending: true` and an empty agent name; detection caught up about four
  seconds later. `Gateway.StartAgent` polls `agent.list` until the pane carries
  an agent whose kind Herdr has resolved. It must not wait on `launch_pending`,
  which pi never clears.
- **`agent.prompt` refuses a freshly started pane.** It returns
  `agent_not_ready: ... is not an active named agent` for every agent started
  through `agent.start`, while the pane accepts input normally. Verified on the
  same pane: `agent.prompt` refused, `pane.send_text` + `pane.send_keys` worked
  and pi responded. `Gateway.Prompt` therefore falls back to the pane-level path
  on exactly that code, for exactly that reason.

### Dialog parsing rules

Rules that keep it from inventing buttons:

- The block must start at `1.` and run to the end of the screen with nothing
  after it but blanks, rules, indented descriptions, the kind's footers and a
  bare `Submit` row. Prose that *follows* the list ends the block — that is
  the case that stops a transcript list from growing buttons.
- Numbers must be 1, 2, 3, … with no gap.
- 2 to 9 real options. A tenth option gets no buttons rather than nine wrong
  ones, because `agent.send_keys` cannot send a two-digit answer.
- A cursor glyph only counts for kinds that draw it, so a quoted shell
  prompt `› 1. hello` does not become a dialog for claude.
- The yes/no marker must **end** a line, and must be within the bottom twelve
  non-empty lines. Both guards came from a real false positive: a plain
  substring test for `[y/n]` grew Yes/No buttons on a working agent whose screen
  happened to be showing this plugin's own source files. An agent explaining
  `[y/n]` in prose is not an agent asking a question.

### Screen reads

`Gateway.ReadForDialog` and `Gateway.ReadScrollback` walk an ordered source
list and stop at the first success, returning early for any error that a
different source cannot fix (missing pane, dead socket). Only
`agent_not_idle` is worth retrying differently — that is the error that made
the old plugin blind.

### Subscription lifetime

`Gateway.Subscribe` owns a reconnect loop. Herdr pushes the current status of
each subscribed pane as soon as the subscription starts, so the stream carries
its own snapshot. After a reconnect it emits `domain.EventResubscribed` — a
plugin-invented event, never sent by Herdr — so the consumer re-reads
`agent.list` instead of trusting a cache that missed events.

## What is built

Verified by `make test` (Go unit tests plus the pi extension tests) and by live
tests that skip without a socket:

- `domain`: kind mapping, status mapping, the dialog parser for all four kinds
  plus the rejection cases.
- `adapters/herdr`: ping, `agent.list`, all four read sources, event
  subscription, `integration.list` — all exercised against the running Herdr.
- `pi-extension/`: the `herdr:blocked` emitter, with tests pinning the
  nesting, underflow, headless-mode and shutdown behaviour.
- `cmd/herdr-tg probe` / `integrations` / `install-extension`.
- `adapters/telegram` + `app`: the bridge daemon, against an httptest fake
  of the Bot API and fake ports — `setup`, `daemon`, a pinned General
  dashboard, topic create/reopen/delete, status icons, the blocked question
  with buttons, the button state machine (digit, multi-select toggle,
  submit, free-text entry, stale presses), done notifications, quiet mode
  (desk vs away detection), and topic deletion on session close. Zero
  third-party dependencies: the client is `net/http` + `encoding/json`, wire
  shapes lifted from the plugin this one replaces.

And end to end, by `make e2e`, which creates a tab, starts a real pi agent,
installs a temporary fixture extension that opens `ctx.ui.confirm`, and drives
the whole chain:

```
started w5Z:pB kind=pi status=idle
agent.prompt refused the pane as not ready, falling back to pane input
event pane.agent_status_changed status=blocked
BLOCKED observed via the event stream
agent.list says status=blocked blocked reason="" labels=map[]
screen source=detection bytes=1898 dialog style=cursor
  title="Herdr bridge self-test · Continue with the bridge test?" choices=2
  button [1] Yes        keys=[enter]
  button [2] No         keys=[down enter]
event pane.agent_status_changed status=idle
closed pane w5Z:pB
```

That is the whole original problem, solved and observed: a pi agent blocks, the
plugin learns about it, and it has two buttons with the right key sequences.

### The pi emitter

pi's `ui_prompt_start` / `ui_prompt_end` events are the exact fit. The docs
describe them as firing "so host/status integrations can report 'waiting for
user' instead of just 'running'", and pi's own type definitions confirm the
payload:

```ts
export interface UIPromptStartEvent {
  type: "ui_prompt_start";
  reason: "ui_prompt";
  kind: UIPromptKind;      // select | confirm | input | editor | custom
  title?: string;
}
```

The emitter republishes these as `herdr:blocked {active, label}` — the shape
Herdr's integration reads — and deliberately does **not** call
`pane.report_agent` itself. Herdr's extension already tracks `agentActive`
from pi's agent events, orders reports with `seq`, carries the session
reference, and knows the authority rules for when a self-report overrides
screen detection. Reporting in parallel would fight it over `seq`. Supplying
the one missing input is the smaller change.

```
$ ./bin/herdr-tg probe
herdr 0.9.1 · protocol 22 (this build targets 22)
socket /Users/example/.config/herdr/herdr.sock

⚡  w5Z:p1  π - task
  kind=pi     profiled
  blocked source: needs the companion pi extension to emit herdr:blocked
  screen: 2421 bytes from "detection", no dialog at the bottom
  -> posted as plain text; the operator answers by typing
```

That last line is the honest answer for a non-blocked pane: the parser
declines to invent buttons.

## Not built yet

1. **Attachments.** A file sent into a topic gets an honest "not supported
   yet" reply; downloading and prompting with paths is inbox work.
2. **Windows.** `dial_windows.go` fails deliberately: Herdr reaches its server
   over a named pipe there and this has never been run against it. A build
   that compiles and then silently cannot talk to Herdr is worse than one that
   says so.
