package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func testServer(t *testing.T, handle func(net.Conn, request)) string {
	t.Helper()
	path := filepath.Join("/tmp", "herdr-test-"+filepath.Base(t.TempDir())+".sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
		_ = os.Remove(path)
	})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var req request
				if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err == nil {
					handle(conn, req)
				}
			}()
		}
	}()
	return path
}

func writeResult(t *testing.T, conn net.Conn, id string, result any) {
	t.Helper()
	line, err := json.Marshal(map[string]any{"id": id, "result": result})
	if err != nil {
		t.Error(err)
		return
	}
	_, _ = conn.Write(append(line, '\n'))
}

func TestReadForDialogFallsBackWhenDetectionHasNoDialog(t *testing.T) {
	path := testServer(t, func(conn net.Conn, req request) {
		params := req.Params.(map[string]any)
		text := "working"
		if params["source"] == "visible" {
			text = "Pick one\n\n→ Yes\n  No\n\n↑↓ navigate  enter select"
		}
		writeResult(t, conn, req.ID, map[string]any{"read": map[string]any{"text": text}})
	})
	g := NewGateway(path, nil)
	screen, source, dialog, err := g.ReadForDialog(context.Background(), "p1", domain.KindPi, 60)
	if err != nil {
		t.Fatal(err)
	}
	if source != domain.ScreenVisible || screen.Text == "" || !dialog.Usable() {
		t.Fatalf("source=%q dialog=%+v screen=%q", source, dialog, screen.Text)
	}
}

func TestSubscribeIncludesGlobalLifecycleEventsWithoutPanes(t *testing.T) {
	got := make(chan []subscription, 1)
	path := testServer(t, func(conn net.Conn, req request) {
		if req.Method != "events.subscribe" {
			return
		}
		body, _ := json.Marshal(req.Params)
		var params eventsSubscribeParams
		_ = json.Unmarshal(body, &params)
		got <- params.Subscriptions
		writeResult(t, conn, req.ID, subscriptionStartedResult{Type: "subscription_started"})
		<-time.After(100 * time.Millisecond)
	})
	g := NewGateway(path, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := g.Subscribe(ctx, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case subs := <-got:
		if len(subs) != 2 || subs[0].Type != string(domain.EventAgentDetected) || subs[1].Type != string(domain.EventPaneClosed) {
			t.Fatalf("subscriptions = %+v", subs)
		}
		for _, sub := range subs {
			if sub.PaneID != "" {
				t.Fatalf("global subscription has pane id: %+v", sub)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("no subscription request")
	}
}

func TestSendKeysFallsBackWhenAgentNotReady(t *testing.T) {
	var methods []string
	path := testServer(t, func(conn net.Conn, req request) {
		methods = append(methods, req.Method)
		if req.Method == "agent.send_keys" {
			line, _ := json.Marshal(map[string]any{
				"id":    req.ID,
				"error": map[string]any{"code": CodeAgentNotReady, "message": "agent is not an active named agent"},
			})
			_, _ = conn.Write(append(line, '\n'))
			return
		}
		if req.Method == "pane.send_keys" {
			writeResult(t, conn, req.ID, map[string]any{"sent": true})
			return
		}
	})
	g := NewGateway(path, nil)
	if err := g.SendKeys(context.Background(), "p1", []string{"enter"}); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if len(methods) != 2 || methods[0] != "agent.send_keys" || methods[1] != "pane.send_keys" {
		t.Fatalf("methods called = %v, want [agent.send_keys pane.send_keys]", methods)
	}
}
