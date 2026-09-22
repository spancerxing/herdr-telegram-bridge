package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func testLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestConfigPathResolution(t *testing.T) {
	if got := ConfigPath("/explicit/config.json"); got != "/explicit/config.json" {
		t.Fatalf("explicit path = %q", got)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "/plugin/cfg")
	if got := ConfigPath(""); got != filepath.Join("/plugin/cfg", "config.json") {
		t.Fatalf("plugin dir = %q", got)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()
	if got := ConfigPath(""); got != standaloneConfigPath(home) {
		t.Fatalf("default = %q", got)
	}
}

func TestStandaloneConfigPath(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, ".config", "herdr-telegram-bridge", "config.json")
	if got := standaloneConfigPath(dir); got != want {
		t.Fatalf("standalone config = %q, want %q", got, want)
	}
}

func TestConfigRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	if _, err := LoadConfig(path, testLog()); err == nil || !strings.Contains(err.Error(), "herdr-tg setup") {
		t.Fatalf("missing file should point at setup, got %v", err)
	}

	if err := SaveConfig(path, Config{BotToken: "T", ChatID: -100, OperatorIDs: []int64{7}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config perms = %v, want 0600", st.Mode().Perm())
	}
	cfg, err := LoadConfig(path, testLog())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.BotToken != "T" || cfg.ChatID != -100 || len(cfg.OperatorIDs) != 1 || cfg.OperatorIDs[0] != 7 {
		t.Fatalf("roundtrip = %+v", cfg)
	}
	if ops := Operators(cfg); len(ops) != 1 || !domain.Allowed(7, ops) || domain.Allowed(8, ops) {
		t.Fatalf("operators = %+v", ops)
	}
}

func TestConfigValidationErrors(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, json, want string
	}{
		{"no token", `{"chat_id":-100,"operator_ids":[1]}`, "bot_token"},
		{"no chat", `{"bot_token":"T","operator_ids":[1]}`, "chat_id"},
		{"no operators", `{"bot_token":"T","chat_id":-100}`, "operator_ids"},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name+".json")
		if err := os.WriteFile(path, []byte(c.json), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(path, testLog())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error naming %q, got %v", c.name, c.want, err)
		}
	}
}

func TestMappingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewMappingStore(dir, testLog())

	m := store.Load()
	if len(m.Topics) != 0 {
		t.Fatalf("missing file should start empty, got %v", m.Topics)
	}
	m.ChatID = -100
	m.Link("w1:p1", &Entry{ThreadID: 5, Name: "agent", Status: domain.StatusWorking})
	if err := store.Save(m); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded := NewMappingStore(dir, testLog()).Load()
	thread, ok := loaded.ThreadFor("w1:p1")
	if !ok || thread != 5 {
		t.Fatalf("thread = %d %v", thread, ok)
	}
	if pane, ok := loaded.PaneForThread(5); !ok || pane != "w1:p1" {
		t.Fatalf("pane = %q %v", pane, ok)
	}
}

func TestMappingThreadForSkipsClosed(t *testing.T) {
	m := &Mapping{Topics: map[string]*Entry{}}
	m.Link("w1:p1", &Entry{ThreadID: 5, Closed: true})
	if _, ok := m.ThreadFor("w1:p1"); ok {
		t.Fatal("a closed topic is not routable")
	}
}

func TestMappingCorruptFileIsSetAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewMappingStore(dir, testLog()).Load()
	if len(m.Topics) != 0 {
		t.Fatalf("corrupt file should start empty, got %v", m.Topics)
	}
	broken, err := filepath.Glob(filepath.Join(dir, "mapping.json.broken-*"))
	if err != nil || len(broken) != 1 {
		t.Fatalf("the corrupt file should be kept under a new name, glob = %v (%v)", broken, err)
	}
}

func TestMappingOrphans(t *testing.T) {
	m := &Mapping{Topics: map[string]*Entry{
		"w1:p1": {ThreadID: 1},
		"w1:p2": {ThreadID: 2, Closed: true},
		"w1:p3": {ThreadID: 3},
	}}
	got := m.Orphans(map[string]bool{"w1:p3": true})
	if len(got) != 1 || got[0] != "w1:p1" {
		t.Fatalf("orphans = %v, want [w1:p1]", got)
	}
}
