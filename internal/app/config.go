// Package app wires the Herdr and Telegram ports into the daemon: one
// forum topic per agent, questions as buttons, answers both ways.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// Config is the daemon's on-disk configuration.
type Config struct {
	BotToken    string  `json:"bot_token"`
	ChatID      int64   `json:"chat_id"`
	OperatorIDs []int64 `json:"operator_ids"`
}

// PluginID is this plugin's Herdr registration id. It must match `id` in
// herdr-plugin.toml: the id is also the last segment of the config directory
// Herdr hands the plugin, so a mismatch silently sends the daemon to a
// different, empty directory. TestPluginIDMatchesManifest guards the pair.
const PluginID = "spancerxing.telegram-bridge"

// ConfigPath resolves the config file: an explicit --config path, then the
// directory Herdr hands a plugin it launches, then that same directory derived
// from home for a direct CLI invocation.
func ConfigPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	if dir := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return pluginConfigPath(home)
}

// pluginConfigPath is where Herdr keeps a plugin's config:
// ~/.config/herdr/plugins/config/<plugin id>/config.json. Herdr passes the same
// directory in HERDR_PLUGIN_CONFIG_DIR when it runs an action; deriving it here
// is what makes a hand-typed `herdr-tg setup` write the file Herdr will later
// read, instead of a second config nobody looks at.
func pluginConfigPath(home string) string {
	return filepath.Join(home, ".config", "herdr", "plugins", "config", PluginID, "config.json")
}

const configExample = `{"bot_token":"123456:ABC…","chat_id":-1001234567890,"operator_ids":[12345678]}`

// LoadConfig reads and validates the config, failing with an error that
// names the path and the expected shape.
func LoadConfig(path string, log *slog.Logger) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("no config at %s — run `herdr-tg setup`, or create it as:\n  %s", path, configExample)
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	switch {
	case cfg.BotToken == "":
		return cfg, fmt.Errorf("%s: bot_token is empty", path)
	case cfg.ChatID == 0:
		return cfg, fmt.Errorf("%s: chat_id is empty", path)
	case len(cfg.OperatorIDs) == 0:
		return cfg, fmt.Errorf("%s: operator_ids is empty", path)
	}
	if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
		log.Warn("config file permissions are wider than 0600; it holds the bot token",
			slog.String("path", path))
	}
	return cfg, nil
}

// SaveConfig writes the config atomically with private permissions.
func SaveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

// Operators maps the configured ids onto the type Allowed checks.
func Operators(cfg Config) []domain.Operator {
	ops := make([]domain.Operator, len(cfg.OperatorIDs))
	for i, id := range cfg.OperatorIDs {
		ops[i] = domain.Operator{ID: id}
	}
	return ops
}

// writeFileAtomic writes via a temp file in the same directory and renames,
// so a reader never sees a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
