package herdr

import (
	"context"
	"net"
	"reflect"
	"sync"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func TestReadForDialogKeepsCodexQuestionCollapsed(t *testing.T) {
 const pending = "• Queued follow-up inputs\n  ? 1 question\n    shift + ← to answer\n› Ask Codex to do anything"
 path := testServer(t, func(conn net.Conn, req request) {
  if req.Method != "agent.read" { t.Errorf("read must not send keys: %s", req.Method) }
  writeResult(t, conn, req.ID, map[string]any{"read": map[string]any{"text": pending}})
 })
 g := NewGateway(path, nil)
 screen, source, dialog, err := g.ReadForDialog(context.Background(), "p1", domain.KindCodex, 60)
 if err != nil || source != domain.ScreenVisible || screen.Text != pending || dialog.Style != domain.StyleQueued {
  t.Fatalf("collapsed question: source=%s dialog=%+v err=%v", source, dialog, err)
 }
}

func TestReadForDialogDoesNotOpenHistoricalHint(t *testing.T) {
	path := testServer(t, func(conn net.Conn, req request) {
		if req.Method != "agent.read" {
			t.Errorf("must not send keys for stale history: %s", req.Method)
			writeResult(t, conn, req.ID, map[string]any{})
			return
		}
		text := "• Queued follow-up inputs\n  ? 1 question\n    shift + ← to answer"
		if req.Params.(map[string]any)["source"] == "visible" {
			text = "Done.\n› Ask Codex to do anything"
		}
		writeResult(t, conn, req.ID, map[string]any{"read": map[string]any{"text": text}})
	})
	g := NewGateway(path, nil)
	_, _, dialog, err := g.ReadForDialog(context.Background(), "p1", domain.KindCodex, 60)
	if err != nil || dialog.Usable() {
		t.Fatalf("historical hint became dialog: %+v, err=%v", dialog, err)
	}
}

func TestTypeAndSubmitUsesPaneInput(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	path := testServer(t, func(conn net.Conn, req request) {
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, req.Method)
		params := req.Params.(map[string]any)
		if req.Method == "pane.send_text" && params["text"] != "my answer" {
			t.Errorf("wrong text: %v", params)
		}
		if req.Method == "pane.send_keys" && !reflect.DeepEqual(params["keys"], []any{"enter"}) {
			t.Errorf("wrong submit keys: %v", params)
		}
		writeResult(t, conn, req.ID, map[string]any{})
	})
	g := NewGateway(path, nil)
	if err := g.TypeAndSubmit(context.Background(), "p1", "my answer"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(methods, []string{"pane.send_text", "pane.send_keys"}) {
		t.Fatalf("input methods: %v", methods)
	}
}
