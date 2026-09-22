package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// dashboardText is deliberately plain text: it is edited in place and must
// remain valid even when an agent supplies arbitrary terminal text.
func dashboardText(agents map[string]domain.Agent) string {
	lines := []string{"📊 Herdr agents", ""}
	list := make([]domain.Agent, 0, len(agents))
	for _, a := range agents {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].DisplayName() == list[j].DisplayName() {
			return list[i].PaneID < list[j].PaneID
		}
		return list[i].DisplayName() < list[j].DisplayName()
	})
	if len(list) == 0 {
		return strings.Join(append(lines, "No active agents."), "\n")
	}
	for _, a := range list {
		line := fmt.Sprintf("%s %s — %s", a.Status.Emoji(), a.DisplayName(), a.Status)
		if reason := a.BlockedReason(); reason != "" {
			line += ": " + reason
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// refreshDashboard creates the one General-topic status message once, then
// edits it in place. Its id and pin state live in mapping.json so restarts do
// not create another dashboard.
func (d *Daemon) refreshDashboard(ctx context.Context) {
	text := dashboardText(d.agents)
	if d.mapping.DashboardMessageID == 0 {
		id, err := d.tg.Send(ctx, domain.Outgoing{ChatID: d.chatID, Text: text})
		if err != nil {
			d.log.Warn("create dashboard", "err", err)
			return
		}
		d.mapping.DashboardMessageID = id
		d.mapping.DashboardPinned = false
	} else if text != d.dashboardText {
		if err := d.tg.EditText(ctx, d.chatID, d.mapping.DashboardMessageID, text, false); err != nil {
			d.log.Warn("update dashboard", "err", err)
			return
		}
	}
	if !d.mapping.DashboardPinned {
		if err := d.tg.Pin(ctx, d.chatID, d.mapping.DashboardMessageID); err != nil {
			d.log.Warn("pin dashboard", "err", err)
			return
		}
		d.mapping.DashboardPinned = true
	}
	d.dashboardText = text
}
