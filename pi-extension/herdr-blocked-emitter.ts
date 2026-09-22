// @herdr-telegram-multi-managed
// Reinstalling overwrites this file. Put your own extensions beside it.
/**
 * herdr-blocked-emitter — tells Herdr when pi is waiting on the user.
 *
 * Why this file has to exist
 * -------------------------
 * Herdr ships an integration for pi at
 * `~/.pi/agent/extensions/herdr-agent-state.ts` (installed by
 * `herdr integration install pi`). That extension is a *consumer*:
 *
 *     pi.events.on("herdr:blocked", (data) => {
 *       if (!data?.active) { blockedCount -= 1; ... return; }
 *       blockedCount += 1; blockedMessage = data.label;
 *       publishState();          // -> pane.report_agent {state:"blocked"}
 *     });
 *
 * `pi.events` is pi's inter-extension event bus (docs/extensions.md,
 * "Inter-extension events"). Nothing in pi itself emits `herdr:blocked` —
 * `grep -r herdr` over the pi package returns nothing — so with only Herdr's
 * extension installed, pi can never report `blocked`, and a Telegram bridge
 * never learns that pi is asking a question.
 *
 * This extension is the producer that was missing. It listens for pi's
 * `ui_prompt_start` / `ui_prompt_end`, which the pi docs describe as firing
 * "so host/status integrations can report 'waiting for user' instead of just
 * 'running'" — exactly this use case — and republishes them on the bus in the
 * shape Herdr's extension expects.
 *
 * Why not report to Herdr directly
 * --------------------------------
 * This extension could call `pane.report_agent` on the socket itself, and
 * would then not depend on Herdr's extension being installed. It deliberately
 * does not. Herdr's extension owns the whole state machine — it tracks
 * agentActive from pi's agent_start/agent_end, orders reports with a
 * monotonically increasing `seq`, carries the session reference for transcript
 * reads, and knows the authority rules that govern when a self-report
 * overrides Herdr's own screen detection. Reporting in parallel would fight it
 * over `seq` and could suppress detection. Supplying the one missing input is
 * the smaller and safer change.
 *
 * Install
 * -------
 *     herdr integration install pi          # the consumer
 *     make install-pi-extension             # this producer
 *
 * Both are needed. `herdr-tg integrations` reports whether each is in place.
 */

// UIPromptStartEvent is pi's own exported type for the event this extension
// consumes; importing it rather than restating the shape means a change in pi
// is a type error here instead of a silent misread at runtime.
import type { ExtensionAPI, UIPromptStartEvent } from "@earendil-works/pi-coding-agent";

/** The event name Herdr's pi integration listens on. Not ours to choose. */
const BLOCKED_EVENT = "herdr:blocked";

/** The payload shape Herdr's `herdr:blocked` handler reads. */
interface BlockedPayload {
  active: boolean;
  label?: string;
}

/**
 * enabled reports whether this process is a pane Herdr is running.
 *
 * Herdr injects HERDR_ENV=1 and HERDR_PANE_ID into every pane it manages.
 * Without both there is no Herdr to talk to, so the extension does nothing
 * rather than emitting events nobody consumes.
 */
function enabled(): boolean {
  return process.env.HERDR_ENV === "1" && Boolean(process.env.HERDR_PANE_ID);
}

/**
 * labelFor gives Herdr's consumer a readable status message. Herdr 0.9.1 does
 * not expose that message through agent.list, so the bridge still reads the
 * screen for the question and options.
 */
function labelFor(event: UIPromptStartEvent): string {
  const title = typeof event.title === "string" ? event.title.trim() : "";
  const kind = typeof event.kind === "string" ? event.kind : "";
  if (title && kind) return `${kind}: ${title}`;
  if (title) return title;
  if (kind) return `waiting for ${kind}`;
  return "waiting for you";
}

export default function (pi: ExtensionAPI) {
  if (!enabled()) {
    return;
  }

  /**
   * Whether Herdr's consumer has seen this session start in TUI mode.
   *
   * Herdr's extension ignores `herdr:blocked` until its own `session_start`
   * handler has run with `ctx.mode === "tui"` (it sets `rootSession` there,
   * and bails for RPC/JSON/print modes because those have no PTY Herdr can
   * display). Emitting before that is harmless but pointless, so this mirrors
   * the same gate to keep the logs honest.
   */
  let activeSession = false;

  /**
   * Depth of open prompt spans.
   *
   * pi documents that nested or overlapping prompts are coalesced into one
   * outer waiting span, so start/end should already be balanced. The counter
   * is defensive: an unpaired `ui_prompt_end` must not underflow Herdr's own
   * count and un-block a pane that is still waiting.
   */
  let depth = 0;

  pi.on("session_start", async (_event, ctx) => {
    if (depth > 0) {
      pi.events.emit(BLOCKED_EVENT, { active: false } satisfies BlockedPayload);
      depth = 0;
    }
    // TUI only: the headless modes have no screen for Herdr to mirror, and
    // Herdr's own integration declines them for the same reason.
    activeSession = ctx?.mode === "tui";
  });

  pi.on("ui_prompt_start", async (event, _ctx) => {
    if (!activeSession) return;
    depth += 1;
    if (depth > 1) {
      // Herdr coalesces nested spans, so only the outermost reports.
      return;
    }
    const payload: BlockedPayload = { active: true, label: labelFor(event) };
    pi.events.emit(BLOCKED_EVENT, payload);
  });

  pi.on("ui_prompt_end", async (_event, _ctx) => {
    if (!activeSession) return;
    if (depth === 0) {
      // An end without a start: nothing to close, and emitting would make
      // Herdr's count go negative.
      return;
    }
    depth -= 1;
    if (depth > 0) {
      return;
    }
    const payload: BlockedPayload = { active: false };
    pi.events.emit(BLOCKED_EVENT, payload);
  });

  pi.on("session_shutdown", async () => {
    // A prompt still open at shutdown would otherwise leave the pane
    // reported as blocked forever, because Herdr's extension has no way to
    // observe that its counterpart went away.
    if (depth > 0) {
      depth = 0;
      pi.events.emit(BLOCKED_EVENT, { active: false } satisfies BlockedPayload);
    }
    activeSession = false;
  });
}
