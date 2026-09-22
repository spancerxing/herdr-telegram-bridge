// Tests for the herdr:blocked emitter.
//
// Run with:  bun test pi-extension/
//
// These drive the extension through a fake ExtensionAPI rather than a real pi
// session, because the contract that matters is narrow and easy to assert: for
// every prompt pi opens, exactly one `herdr:blocked {active:true}` must reach
// the bus, and for every prompt it closes, exactly one `{active:false}`. A
// missing `active:false` leaves a Herdr pane reported as blocked forever, and
// a duplicate `active:false` un-blocks a pane that is still waiting — both are
// silent failures in the real system, so both are pinned here.

import { afterEach, beforeEach, describe, expect, test } from "bun:test";

import extension from "./herdr-blocked-emitter.ts";

interface Emitted {
  name: string;
  data: unknown;
}

interface Fake {
  pi: any;
  emitted: Emitted[];
  /** Fire a registered handler for an event name. */
  fire(name: string, event?: unknown, ctx?: unknown): Promise<void>;
}

function fakePi(): Fake {
  const handlers = new Map<string, Array<(e: unknown, c: unknown) => unknown>>();
  const emitted: Emitted[] = [];
  const pi = {
    on(name: string, handler: (e: unknown, c: unknown) => unknown) {
      const list = handlers.get(name) ?? [];
      list.push(handler);
      handlers.set(name, list);
      return () => {};
    },
    events: {
      on() {
        return () => {};
      },
      emit(name: string, data: unknown) {
        emitted.push({ name, data });
      },
    },
  };
  return {
    pi,
    emitted,
    async fire(name, event = {}, ctx = {}) {
      for (const h of handlers.get(name) ?? []) {
        await h(event, ctx);
      }
    },
  };
}

const TUI = { mode: "tui" };

describe("herdr-blocked-emitter", () => {
  const savedEnv = { ...process.env };

  beforeEach(() => {
    // The extension is a no-op unless Herdr put it in a pane.
    process.env.HERDR_ENV = "1";
    process.env.HERDR_PANE_ID = "w5Z:p1";
  });

  afterEach(() => {
    process.env = { ...savedEnv };
  });

  test("emits nothing when not running inside a Herdr pane", async () => {
    delete process.env.HERDR_ENV;
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "confirm", title: "Allow?" }, TUI);
    expect(f.emitted).toEqual([]);
  });

  test("a prompt start reports blocked with the title and kind", async () => {
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "confirm", title: "Allow rm -rf?" }, TUI);

    expect(f.emitted).toHaveLength(1);
    expect(f.emitted[0]!.name).toBe("herdr:blocked");
    expect(f.emitted[0]!.data).toEqual({ active: true, label: "confirm: Allow rm -rf?" });
  });

  test("the prompt end clears blocked exactly once", async () => {
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "select", title: "Pick" }, TUI);
    await f.fire("ui_prompt_end", {}, TUI);

    expect(f.emitted.map((e) => e.data)).toEqual([
      { active: true, label: "select: Pick" },
      { active: false },
    ]);
  });

  test("a prompt with no title still reports something readable", async () => {
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "input" }, TUI);
    expect(f.emitted[0]!.data).toEqual({ active: true, label: "waiting for input" });
  });

  test("an unpaired end cannot underflow", async () => {
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    // Herdr's own handler decrements a counter on active:false, so a stray
    // end would drive it negative and un-block a waiting pane.
    await f.fire("ui_prompt_end", {}, TUI);
    await f.fire("ui_prompt_end", {}, TUI);
    expect(f.emitted).toEqual([]);
  });

  test("nested prompts report one span, not two", async () => {
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "confirm", title: "outer" }, TUI);
    await f.fire("ui_prompt_start", { kind: "select", title: "inner" }, TUI);
    // Only the outermost span is announced; Herdr coalesces nested spans.
    expect(f.emitted).toHaveLength(1);
    await f.fire("ui_prompt_end", {}, TUI);
    // The inner close must not clear blocked while the outer is still open.
    expect(f.emitted).toHaveLength(1);
    await f.fire("ui_prompt_end", {}, TUI);
    expect(f.emitted).toHaveLength(2);
    expect(f.emitted[1]!.data).toEqual({ active: false });
  });

  test("a new session resets an unclosed prompt", async () => {
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "confirm", title: "old" }, TUI);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "confirm", title: "new" }, TUI);

    expect(f.emitted.map((e) => e.data)).toEqual([
      { active: true, label: "confirm: old" },
      { active: false },
      { active: true, label: "confirm: new" },
    ]);
  });

  test("headless modes never report blocked", async () => {
    const f = fakePi();
    extension(f.pi);
    // Herdr's consumer ignores these modes too, so emitting would be noise.
    await f.fire("session_start", {}, { mode: "json" });
    await f.fire("ui_prompt_start", { kind: "confirm", title: "Allow?" }, { mode: "json" });
    expect(f.emitted).toEqual([]);
  });

  test("shutdown clears a prompt left open", async () => {
    const f = fakePi();
    extension(f.pi);
    await f.fire("session_start", {}, TUI);
    await f.fire("ui_prompt_start", { kind: "confirm", title: "Allow?" }, TUI);
    await f.fire("session_shutdown", {}, TUI);

    expect(f.emitted.map((e) => e.data)).toEqual([{ active: true, label: "confirm: Allow?" }, { active: false }]);
  });
});
