// Command herdr-tg is the plugin binary: the probe verifies the Herdr side,
// and the daemon carries the Telegram bridge.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/adapters/herdr"
	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

const usage = `Telegram Bridge — bridge Herdr agents to Telegram

usage:
  herdr-tg probe [--socket PATH] [--watch SECONDS]
  herdr-tg integrations
  herdr-tg install-extension [--dry-run] [--force]
  herdr-tg setup [--config PATH]
  herdr-tg startup [--socket PATH] [--config PATH] [--verbose]
  herdr-tg daemon [--socket PATH] [--config PATH] [--verbose]
  herdr-tg version

probe        Ping Herdr, list the agents, read each one's screen and show the
             buttons the dialog parser would build for it. This is the
             fastest way to see whether a kind can be answered from Telegram.
integrations Show which of the four profiled kinds have their state hook
             installed, and whether the pi emitter is in place.
install-extension
             Write the pi extension that pi needs before it can report
             blocked. Edits ~/.pi/agent/extensions/, so it is a separate
             explicit step and refuses to overwrite a file it does not own.
setup        Ask for a bot token, have you add the bot to a forum supergroup,
             and write the config (bot_token, chat_id, operator_ids).
daemon       Run the bridge: one forum topic per agent, blocked questions as
             buttons, answers and free text both ways. Needs setup first.
startup      Start a single background bridge; exits when Herdr stays offline.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	switch args[0] {
	case "probe":
		return probe(ctx, args[1:], log)
	case "integrations":
		return integrations(ctx, log)
	case "install-extension":
		return installPiExtension(hasFlag(args, "dry-run"), hasFlag(args, "force"))
	case "setup":
		return runSetup(ctx, args[1:], log)
	case "daemon":
		return runDaemon(ctx, args[1:], log)
	case "startup":
		return runStartup(ctx, args[1:], log)
	case "version":
		fmt.Printf("protocol %d (targets herdr 0.9.1)\n", herdr.ProtocolVersion)
		return nil
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

// flagValue reads "--name VALUE" or "--name=VALUE" from args.
func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == "--"+name && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(a, "--"+name+"=") {
			return strings.TrimPrefix(a, "--"+name+"="), true
		}
	}
	return "", false
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--"+name {
			return true
		}
	}
	return false
}

// flagFirst returns a flag's value, or "" when absent.
func flagFirst(args []string, name string) string {
	v, _ := flagValue(args, name)
	return v
}

func newGateway(args []string, log *slog.Logger) *herdr.Gateway {
	socket, _ := flagValue(args, "socket")
	return herdr.NewGateway(socket, log)
}

func probe(ctx context.Context, args []string, log *slog.Logger) error {
	gw := newGateway(args, log)

	info, err := gw.Ping(ctx)
	if err != nil {
		return fmt.Errorf("cannot reach Herdr at %s: %w", gw.Path(), err)
	}
	fmt.Printf("herdr %s · protocol %d (this build targets %d)\n", info.Version, info.Protocol, herdr.ProtocolVersion)
	if info.Protocol != herdr.ProtocolVersion {
		fmt.Printf("  ! protocol differs; a method this build relies on may have changed\n")
	}
	fmt.Printf("socket %s\n\n", gw.Path())

	agents, err := gw.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("agent.list: %w", err)
	}
	if len(agents) == 0 {
		fmt.Println("no agents running")
	}

	for _, a := range agents {
		describe(ctx, gw, a)
	}

	if watch, ok := flagValue(args, "watch"); ok {
		d, err := time.ParseDuration(watch)
		if err != nil {
			return fmt.Errorf("--watch %q: %w", watch, err)
		}
		return watchEvents(ctx, gw, agents, d)
	}
	return nil
}

func describe(ctx context.Context, gw *herdr.Gateway, a domain.Agent) {
	profiled := "profiled"
	if !a.Kind.IsProfiled() {
		profiled = "no profile, generic dialog rules"
	}
	fmt.Printf("%s  %s  %s\n", a.Status.Emoji(), a.PaneID, a.DisplayName())
	fmt.Printf("  kind=%-6s %s\n", a.Kind.DisplayName(), profiled)
	fmt.Printf("  cwd=%s\n", a.Cwd)
	if r := a.BlockedReason(); r != "" {
		fmt.Printf("  blocked reason from Herdr: %q\n", r)
	}
	switch {
	case a.Kind.NeedsStateEmitter():
		fmt.Printf("  blocked source: needs the companion pi extension to emit herdr:blocked\n")
	default:
		fmt.Printf("  blocked source: Herdr detection manifest (hook reports session only)\n")
	}

	if a.Status != domain.StatusBlocked {
		fmt.Println()
		return
	}

	// The status is the trigger; parsing only extracts the current dialog.
	screen, src, d, err := gw.ReadForDialog(ctx, a.PaneID, a.Kind, 60)
	if err != nil {
		fmt.Printf("  screen: unreadable (%v)\n\n", err)
		return
	}
	if !d.Usable() {
		fmt.Printf("  screen: %d bytes from %q, no dialog at the bottom\n", len(screen.Text), src)
		fmt.Printf("  -> posted as plain text; the operator answers by typing\n\n")
		return
	}
	fmt.Printf("  dialog: %d option(s) from %q, style=%s multi=%v\n", len(d.Choices), src, d.Style, d.Multi)
	for _, c := range d.Choices {
		fmt.Printf("    [%-2s] %s\n", c.Key, domain.CutLabel(c.Label, 56))
	}
	if d.TextEntry > 0 {
		fmt.Printf("    [✏️] %s (option %d)\n", d.TextLabel, d.TextEntry)
	}
	if d.Multi {
		fmt.Printf("    submit keys: %v\n", d.SubmitKeys())
	}
	fmt.Println()
}

func watchEvents(ctx context.Context, gw *herdr.Gateway, agents []domain.Agent, d time.Duration) error {
	paneIDs := func(agents []domain.Agent) []string {
		panes := make([]string, 0, len(agents))
		for _, a := range agents {
			panes = append(panes, a.PaneID)
		}
		return panes
	}
	subCtx, cancel := context.WithCancel(ctx)
	defer func() { cancel() }()
	ch, err := gw.Subscribe(subCtx, paneIDs(agents))
	if err != nil {
		return fmt.Errorf("events.subscribe: %w", err)
	}
	fmt.Printf("watching %d pane(s) for %s …\n", len(agents), d)
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return errors.New("subscription closed")
			}
			stamp := time.Now().Format("15:04:05")
			switch ev.Kind {
			case domain.EventResubscribed:
				fmt.Printf("%s  (resubscribed; events may have been missed)\n", stamp)
			case domain.EventPaneClosed:
				fmt.Printf("%s  %s  pane closed -> exited\n", stamp, ev.PaneID)
			default:
				fmt.Printf("%s  %s  %s -> %s\n", stamp, ev.PaneID, ev.AgentName, ev.Status)
			}
			if ev.Kind == domain.EventAgentDetected || ev.Kind == domain.EventPaneClosed || ev.Kind == domain.EventResubscribed {
				// ponytail: rebuilding leaves a tiny event gap; use a draining
				// handoff if a future daemon proves the gap matters.
				latest, err := gw.ListAgents(ctx)
				if err != nil {
					return fmt.Errorf("reconcile agent.list: %w", err)
				}
				nextCtx, nextCancel := context.WithCancel(ctx)
				next, err := gw.Subscribe(nextCtx, paneIDs(latest))
				if err != nil {
					nextCancel()
					return fmt.Errorf("events.resubscribe: %w", err)
				}
				cancel()
				subCtx, cancel, ch = nextCtx, nextCancel, next
			}
		case <-deadline:
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

func integrations(ctx context.Context, log *slog.Logger) error {
	gw := newGateway(nil, log)
	list, err := gw.IntegrationList(ctx)
	if err != nil {
		return fmt.Errorf("integration.list: %w", err)
	}
	fmt.Println("kind            state          reports state   install command")
	fmt.Println("--------------------------------------------------------------------------")
	for _, k := range domain.SupportedKinds {
		found := integrationFor(list, k)
		state := "unknown"
		path := ""
		available := true
		if found != nil {
			switch {
			case !found.Available:
				state = "cli missing"
			case found.Installed:
				state = "installed"
			case found.Outdated:
				state = "outdated"
			default:
				state = "not installed"
			}
			path = found.Path
			available = found.Available
		}
		reports := "session only"
		if k.NeedsStateEmitter() {
			reports = "needs emitter"
		}
		cmd := "herdr integration install " + k.CLITarget()
		if !available {
			cmd = "(install " + k.Command() + " first)"
		}
		fmt.Printf("%-15s %-14s %-15s %s\n", k.DisplayName(), state, reports, cmd)
		if path != "" {
			fmt.Printf("%-15s %s\n", "", path)
		}
	}
	fmt.Println()

	// pi needs two things and fails silently when only one is present: Herdr's
	// consumer extension, and the emitter this plugin ships.
	home, err := os.UserHomeDir()
	emitterOK := false
	emitterPath := ""
	if err == nil {
		emitterOK, emitterPath = piExtensionStatus(home)
	}
	fmt.Println("pi blocked support needs two extensions:")
	if found := integrationFor(list, domain.KindPi); found != nil && found.Installed {
		fmt.Println("  [x] Herdr consumer   herdr integration install pi")
	} else {
		fmt.Println("  [ ] Herdr consumer   herdr integration install pi")
	}
	if emitterOK {
		fmt.Printf("  [x] Emitter          %s\n", emitterPath)
	} else {
		fmt.Println("  [ ] Emitter          herdr-tg install-extension")
	}
	if !emitterOK {
		fmt.Println()
		fmt.Println("Without the emitter nothing emits the herdr:blocked event Herdr's")
		fmt.Println("extension waits for, so a pi pane stays 'working' while pi asks a question.")
	}
	fmt.Println()
	fmt.Println("claude, codex and agy report session identity only; their blocked state comes")
	fmt.Println("from Herdr's own detection manifest, so they need no emitter.")
	return nil
}

// integrationFor finds a kind's entry in an integration list.
func integrationFor(list []domain.Integration, k domain.Kind) *domain.Integration {
	for i := range list {
		if list[i].Target == k.APITarget() {
			return &list[i]
		}
	}
	return nil
}
