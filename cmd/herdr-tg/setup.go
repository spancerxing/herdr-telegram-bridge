package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spancerxing/herdr-telegram-bridge/internal/adapters/telegram"
	"github.com/spancerxing/herdr-telegram-bridge/internal/app"
)

// runSetup writes the config interactively: validate the token, then have
// the operator add the bot to the forum group so the ids can be captured
// from the updates that generates.
func runSetup(ctx context.Context, args []string, log *slog.Logger) error {
	cfgPath := app.ConfigPath(flagFirst(args, "config"))
	in := bufio.NewReader(os.Stdin)

	fmt.Print("Bot token (from @BotFather): ")
	token, err := in.ReadString('\n')
	if err != nil && strings.TrimSpace(token) == "" {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("no token given")
	}

	// New validates the token with getMe and clears any webhook so polling
	// can run; no chat is bound yet.
	tg, err := telegram.New(ctx, token, telegram.Config{}, log)
	if err != nil {
		return fmt.Errorf("token check: %w", err)
	}

	fmt.Println()
	fmt.Println("Now, in Telegram:")
	fmt.Println("  1. add the bot to your forum supergroup as an administrator")
	fmt.Println("     (it needs Manage Topics, Delete Messages and Pin Messages),")
	fmt.Println("  2. confirm the promotion, or write any message in the group.")
	fmt.Println()
	fmt.Print("Press Enter once done (Ctrl-C to abort)… ")
	if _, err := in.ReadString('\n'); err != nil {
		return err
	}

	chatID, fromID, err := tg.AwaitChat(ctx)
	if err != nil {
		return fmt.Errorf("no group seen: %w", err)
	}
	if err := app.SaveConfig(cfgPath, app.Config{
		BotToken:    token,
		ChatID:      chatID,
		OperatorIDs: []int64{fromID},
	}); err != nil {
		return err
	}
	fmt.Printf("saved %s\n\n", cfgPath)
	if id := os.Getenv("HERDR_PLUGIN_ID"); id != "" {
		fmt.Printf("start the bridge with: herdr plugin action invoke %s.daemon\n", id)
	} else {
		fmt.Println("start the bridge with: herdr-tg startup")
	}
	return nil
}
