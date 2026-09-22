package app

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// Entry is one agent's topic.
type Entry struct {
	ThreadID int           `json:"thread_id"`
	Name     string        `json:"name"`
	Status   domain.Status `json:"status"`
	Closed   bool          `json:"closed,omitempty"`
}

// Mapping is every pane-to-topic link the daemon owns. All access happens
// on the daemon's single goroutine, so there are no locks.
type Mapping struct {
	Version            int               `json:"version"`
	ChatID             int64             `json:"chat_id"`
	DashboardMessageID int               `json:"dashboard_message_id,omitempty"`
	DashboardPinned    bool              `json:"dashboard_pinned,omitempty"`
	Topics             map[string]*Entry `json:"topics"`
}

// ThreadFor returns the open topic of a pane.
func (m *Mapping) ThreadFor(paneID string) (int, bool) {
	e, ok := m.Topics[paneID]
	if !ok || e.Closed {
		return 0, false
	}
	return e.ThreadID, true
}

func (m *Mapping) PaneForThread(threadID int) (string, bool) {
	for pane, e := range m.Topics {
		if e.ThreadID == threadID {
			return pane, true
		}
	}
	return "", false
}

func (m *Mapping) Link(paneID string, e *Entry) { m.Topics[paneID] = e }

func (m *Mapping) Remove(paneID string) { delete(m.Topics, paneID) }

func (m *Mapping) MarkClosed(paneID string, closed bool) {
	if e, ok := m.Topics[paneID]; ok {
		e.Closed = closed
	}
}

func (m *Mapping) SetStatus(paneID string, status domain.Status) {
	if e, ok := m.Topics[paneID]; ok {
		e.Status = status
	}
}

// Orphans lists mapped panes whose agent is gone and whose topic is still
// open.
func (m *Mapping) Orphans(live map[string]bool) []string {
	var out []string
	for pane, e := range m.Topics {
		if !live[pane] && !e.Closed {
			out = append(out, pane)
		}
	}
	return out
}

// MappingStore persists the mapping next to the config file.
type MappingStore struct {
	path string
	log  *slog.Logger
}

func NewMappingStore(configDir string, log *slog.Logger) *MappingStore {
	return &MappingStore{path: filepath.Join(configDir, "mapping.json"), log: log}
}

// Load reads the mapping; a missing file starts empty and a corrupt one is
// moved aside, because rebuilding topics is recoverable and wedging the
// daemon on bad JSON is not.
func (s *MappingStore) Load() *Mapping {
	m := &Mapping{Version: 1, Topics: map[string]*Entry{}}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return m
	}
	if err != nil {
		s.log.Warn("read mapping", slog.String("path", s.path), slog.String("err", err.Error()))
		return m
	}
	if err := json.Unmarshal(data, m); err != nil {
		broken := fmt.Sprintf("%s.broken-%d", s.path, time.Now().Unix())
		s.log.Warn("mapping corrupt; starting empty and keeping the old file",
			slog.String("path", s.path), slog.String("moved_to", broken), slog.String("err", err.Error()))
		_ = os.Rename(s.path, broken)
		return &Mapping{Version: 1, Topics: map[string]*Entry{}}
	}
	if m.Topics == nil {
		m.Topics = map[string]*Entry{}
	}
	return m
}

// Save writes the mapping atomically.
func (s *MappingStore) Save(m *Mapping) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, append(data, '\n'), 0o644)
}
