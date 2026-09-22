// @herdr-telegram-multi-e2e-fixture
//
// A pi extension used only to test the blocked path end to end.
//
// It exists because a real pi confirmation is hard to trigger on demand from
// outside: the plugin has to observe a genuine `ui_prompt_start`, and the only
// reliable way to produce one is to call `ctx.ui.confirm()` from an extension.
// This fixture registers a command that does exactly that, so an automated test
// can start a pi pane, run the command, and watch for the blocked status that
// Herdr's pi integration reports as a result.
//
// It is NOT installed by the plugin. The e2e test copies it into
// ~/.pi/agent/extensions/ for the duration of a run and removes it afterwards.
// Do not install it permanently: it adds a `/herdr-selftest` command to every
// session for no reason.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  pi.registerCommand("herdr-selftest", {
    description: "Test fixture: open a blocking confirm prompt for the Herdr bridge test",
    handler: async (_args, ctx) => {
      // Stays open until answered, which is the window the test needs.
      const ok = await ctx.ui.confirm("Herdr bridge self-test", "Continue with the bridge test?");
      ctx.ui.notify(ok ? "self-test answered yes" : "self-test answered no", "info");
    },
  });
}
