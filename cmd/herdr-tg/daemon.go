package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/adapters/herdr"
	"github.com/spancerxing/herdr-telegram-bridge/internal/adapters/telegram"
	"github.com/spancerxing/herdr-telegram-bridge/internal/app"
)

// runDaemon runs the bridge until interrupted: config, Herdr, Telegram,
// then the loop.
func runDaemon(ctx context.Context, args []string, log *slog.Logger) error {
	level := slog.LevelInfo
	if hasFlag(args, "verbose") {
		level = slog.LevelDebug
	}
	log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	cfgPath := app.ConfigPath(flagFirst(args, "config"))
	cfg, err := app.LoadConfig(cfgPath, log)
	if err != nil {
		return err
	}
	// Herdr's config may link to the standalone config. Keep both entry
	// points on the same mapping as well as the same daemon lock.
	cfgPath, err = filepath.EvalSymlinks(cfgPath)
	if err != nil {
		return err
	}
	lock, err := daemonLock(cfgPath, hasFlag(args, "inherit-lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lock.Truncate(0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(lock, "%d\n", os.Getpid()); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	gw := herdr.NewGateway(flagFirst(args, "socket"), log)
	info, err := gw.Ping(ctx)
	if err != nil {
		return fmt.Errorf("cannot reach Herdr: %w", err)
	}
	log.Info("herdr connected", slog.String("version", info.Version))
	health := time.NewTicker(time.Second)
	defer health.Stop()
	go func() {
		if err := watchHerdr(ctx, health.C, func(probe context.Context) error {
			_, err := gw.Ping(probe)
			return err
		}); err != nil {
			log.Warn("Herdr stopped; stopping bridge", slog.String("reason", err.Error()))
			cancel()
		}
	}()

	tg, err := telegram.New(ctx, cfg.BotToken, telegram.Config{
		ChatID:    cfg.ChatID,
		Operators: app.Operators(cfg),
	}, log)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}

	store := app.NewMappingStore(filepath.Dir(cfgPath), log)
	d := app.NewDaemon(gw, tg, store, cfg.ChatID, app.Operators(cfg), app.RealClock{}, log)
	log.Info("daemon running", slog.Int64("chat_id", cfg.ChatID), slog.String("config", cfgPath))
	defer log.Info("daemon stopped")
	return d.Run(ctx)
}
