# Telegram Approver

Leave long-running AI tasks unattended and approve their questions from
Telegram. A Herdr plugin for **Claude Code**, **Codex**, **agy** (Antigravity
CLI), and **pi**.

Each agent gets a Telegram topic with approval buttons, a status icon, and
completion notices. A pinned dashboard shows what is running and what needs
your attention. macOS quiet mode silences notifications while you are at
your desk. Full conversation streaming and private-message paging are outside
the scope of this plugin.

> **Status: the blocked path works end to end, and the minimal Telegram
> bridge is built.** Verified on a live Herdr 0.9.1 with a real pi agent: it
> blocks, the plugin sees it, and it produces the right buttons. The bridge
> includes a pinned dashboard, completion notices, macOS quiet mode, and
> topic cleanup. It starts with the Herdr server and stops after a sustained
> server outage. See
> [What works today](#what-works-today).

## Why this exists

The plugin this is modelled on,
[`permgps/herdr-telegram-agents`](https://github.com/permgps/herdr-telegram-agents),
works, but on this machine it never showed a button. Three causes, all
measured — the full analysis is in [docs/DESIGN.md](docs/DESIGN.md):

1. **pi cannot report `blocked` at all.** Herdr's own manifest for pi
   (`pi.toml`) declares two rules and both are `working`. No amount of screen
   parsing will put a pi pane into `blocked`.
2. **The old capture loop asked for 400 lines of scrollback** with
   `source=recent`, which protocol 22 refuses for a busy alternate-screen
   pane. 911 such failures were logged, all on the pi pane — so the plugin was
   blind on exactly the kinds that needed help.
3. **The hook Herdr installs for pi is a consumer, not a producer.** It
   listens for the inter-extension event `herdr:blocked`, and nothing in pi
   emits it. So even installing the integration changes nothing.

The interesting one is the third: `herdr integration install` looks like it
wires up state reporting for every kind, and for claude, codex and agy it
reports **session identity only**. Only pi's hook carries `state` — and it
needs a companion extension to feed it. This repo ships that extension.

## Requirements

- Herdr **0.9.0+** (socket protocol 22). Lower versions do not have
  `recent_unwrapped` or `pane.agent_status_changed`.
- Go 1.27.1+ to build (the version in `go.mod`). The build script looks for
  `go` on `PATH`, then in `~/go-sdk/bin`, `/usr/local/go/bin` and
  `/opt/homebrew/bin`.
- A Telegram bot token and a forum supergroup. Run `herdr-tg setup` to create
  the daemon config.

## Install

```bash
herdr plugin install spancerxing/herdr-telegram-bridge
herdr plugin action invoke spancerxing.telegram-bridge.setup
```

The setup action opens an interactive popup. Enter the token from BotFather,
add the bot to a forum supergroup, and grant Manage Topics, Delete Messages,
and Pin Messages. Finish setup before starting the bridge:

```bash
herdr plugin action invoke spancerxing.telegram-approver.daemon
```

For Pi, also install both state-reporting components, then run `/reload` in
existing Pi sessions:

```bash
herdr integration install pi
herdr plugin action invoke spancerxing.telegram-approver.install-pi-extension
```

The Pi companion is bundled with this plugin; no separate package publication
is needed. The bridge starts automatically on subsequent Herdr server starts.
Use `herdr plugin config-dir spancerxing.telegram-approver` to locate its
configuration. GitHub installation currently builds from source and requires
Go; prebuilt downloads are not provided yet.

## Build

```bash
make build      # -> bin/herdr-tg
make test       # Go tests + pi extension tests
make test-live  # adds the tests that talk to the running Herdr
```

## What works today

```bash
./bin/herdr-tg probe
```

Pings Herdr, lists every agent, reads each one's screen, and reports whether a
question dialog was found and what buttons it would produce:

```
herdr 0.9.1 · protocol 22 (this build targets 22)
socket /Users/example/.config/herdr/herdr.sock

⚡  w5Z:p1  π - task
  kind=pi     profiled
  blocked source: needs the companion pi extension to emit herdr:blocked
  screen: 2421 bytes from "detection", no dialog at the bottom
  -> posted as plain text; the operator answers by typing

✅  w5Z:p4  agy
  kind=agy    profiled
  blocked source: Herdr detection manifest (hook reports session only)
  screen: 4262 bytes from "detection", no dialog at the bottom
```

```bash
./bin/herdr-tg integrations
```

Prints the per-kind truth table — which hook exists, whether it reports state
or only a session, and the exact install command:

```
kind            state          reports state   install command
--------------------------------------------------------------------------
claude          not installed  session only    herdr integration install claude
codex           not installed  session only    herdr integration install codex
agy             not installed  session only    herdr integration install antigravity-cli
pi              not installed  needs emitter   herdr integration install pi

claude, codex and agy report session identity only; their blocked state comes
from Herdr's own detection manifest. pi can report blocked, but only if the
companion pi extension (pi-extension/) emits the herdr:blocked event it listens for.
```

```bash
./bin/herdr-tg probe --watch 30s
```

Streams `pane.agent_status_changed` events — this is the subscription that
replaces the old plugin's one-second screen polling.

```bash
make e2e
```

The end-to-end proof. It creates a tab, starts a real pi agent, installs a
uniquely named temporary fixture extension that opens a blocking `ctx.ui.confirm`,
parses the real cursor dialog, sends the generated **Yes** key sequence, and
asserts that the pane leaves `blocked` again. Current output:

```
started w5Z:pB kind=pi status=idle
agent.prompt refused the pane as not ready, falling back to pane input
event pane.agent_status_changed status=blocked
BLOCKED observed via the event stream
screen source=detection dialog style=cursor
  title="Herdr bridge self-test · Continue with the bridge test?" choices=2
  button [1] Yes        keys=[enter]
  button [2] No         keys=[down enter]
event pane.agent_status_changed status=idle
```

pi's dialog is not a Claude Code dialog: it numbers nothing and marks the
selection with `→`, so the answer is an arrow walk and enter. That shape is
what the `cursor` style exists for, and it was found by driving the real path.

## The bridge daemon

```bash
./bin/herdr-tg setup    # token in, add the bot to a forum group, config out
./bin/herdr-tg daemon   # or: make daemon
```

`setup` validates the token with getMe, then waits for you to add the bot
to a forum supergroup as an administrator; the promotion (or any message
there) hands over the `chat_id` and the first operator id. The config lands
at `~/.config/herdr-telegram-bridge/config.json` (0600), or next to whatever
`--config` points at — when Herdr runs the plugin, it uses the plugin config
dir. `mapping.json` beside it remembers which pane owns which topic.
Standalone installations from the former `herdr-telegram-multi` project keep
using their existing config when no new config exists. The Pi companion keeps
its existing filename and ownership marker so upgrades do not install two
copies of the same event emitter.

The daemon then:

- gives every agent a forum topic, named after the agent, its icon the
  agent's status (from Telegram's default topic-icon pack);
- on `blocked`, posts the question with one button per option — numbered
  dialogs send digits, `[y/n]` sends `y`/`n`, pi's cursor dialog walks the
  arrows and presses enter;
- on `done`, posts a task completion notice (`🏆 任务已完成`) with the output tail;
- quiet mode: checks native macOS idle time (`ioreg`); stays silent while the
  operator is at the desk, rings when away (> 3 min);
- retires the buttons once the question is answered (pressing a stale
  button answers "expired");
- forwards what you type in a topic to that agent;
- deletes an agent's topic and all its messages when its pane closes (falls back to close if deletion is refused), keeping Telegram tidy.

Buttons answer through `dialog.KeysFor`, so every kind is answered the way
its own CLI expects. Non-operators are ignored; what they press gets a
"not allowed" toast. Attachments get an honest "not supported yet".

### Install as a Herdr plugin

`herdr plugin link` skips the manifest's build step, so build once first:

```bash
make build
herdr plugin link .
```

Then `probe` and `integrations` appear as plugin actions.

The startup hook launches one background bridge after Herdr restores its
server session. It exits when Herdr is unreachable continuously for 5 seconds
(checked every second). Brief server handoffs can reconnect. Detaching a UI
client leaves the Herdr server and long-running tasks alive, so the bridge
continues too. A config-scoped OS lock prevents duplicate Telegram pollers,
including when the plugin config is a symlink to the standalone config.

Linking or enabling a plugin does **not** run its startup hook immediately.
To start it in an already-running Herdr session:

```bash
herdr plugin action invoke spancerxing.telegram-approver.daemon
```

Re-run `herdr plugin link .` after changing the manifest. Background logs go
to `daemon.log` in `HERDR_PLUGIN_STATE_DIR` (or beside the canonical config
when started directly with `herdr-tg startup`).

## Layout

```
cmd/herdr-tg/            CLI: probe, integrations, install-extension, setup, daemon
internal/domain/         Kind, Status, dialog parser, Telegram types
internal/ports/          the Herdr and Telegram interfaces the app needs
internal/adapters/herdr/ protocol 22 client, gateway, screen-read fallback
internal/adapters/telegram/ stdlib Bot API client, gateway, long polling
internal/app/            the daemon: topic mapping, blocked flow, both ways
pi-extension/            the herdr:blocked emitter pi needs, plus its tests
docs/DESIGN.md           the measurements behind every decision
```

## The per-kind matrix

The single most important table in this repo:

| kind | `blocked` comes from | dialog shape | state |
|---|---|---|---|
| claude | Herdr detection manifest (6.7 kB of rules) | numbered | untested live |
| codex | Herdr detection manifest (3.2 kB) | numbered + `[y/n]` | untested live |
| agy | Herdr detection manifest (one narrow rule) | numbered | untested live |
| pi | companion pi extension (built here) | **cursor, unnumbered** | **verified end to end** |

## Enabling blocked on pi

pi needs **two** extensions, and fails silently when only one is present:

```bash
herdr integration install pi      # Herdr's consumer: turns events into pane.report_agent
make install-pi-extension         # this repo's emitter: produces the event
```

`./bin/herdr-tg integrations` prints both with a checkbox each.

The emitter listens for pi's `ui_prompt_start` / `ui_prompt_end` — documented as
firing "so host/status integrations can report 'waiting for user'" — and
republishes them on the `herdr:blocked` inter-extension event that Herdr's
integration waits for. It does not talk to the Herdr socket itself, on purpose:
Herdr's extension already owns the state machine, the report `seq` ordering and
the authority rules that decide when a self-report overrides screen detection.

Run its tests with `make test-pi`. They pin the two silent failure modes: a
missing `active:false` leaves a pane reported as blocked forever, and a
duplicate one un-blocks a pane that is still waiting.

## Roadmap

1. File inbox: download attachments and hand them to the agent as paths.
2. Optional secret redactor for multi-operator groups.

## License

MIT. See [LICENSE](LICENSE) and [third-party notices](THIRD_PARTY_NOTICES.md).

For marketplace distribution, see [Publishing](docs/PUBLISHING.md).
