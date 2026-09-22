package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

// newTestGateway connects a Gateway to a scripted fake Bot API. The bot is
// id 42, the group -100, operator 7.
func newTestGateway(t *testing.T) (*Gateway, *fakeAPI) {
	t.Helper()
	f := newFakeAPI(t)
	f.respond("getMe", `{"ok":true,"result":{"id":42,"username":"bridge_test_bot","is_bot":true}}`)
	f.respond("deleteWebhook", `{"ok":true,"result":true}`)
	f.respond("getForumTopicIconStickers", `{"ok":true,"result":[]}`)
	g, err := New(context.Background(), "T", Config{
		ChatID:    -100,
		Operators: []domain.Operator{{ID: 7}},
		BaseURL:   f.server.URL,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if f.count("deleteWebhook") != 1 {
		t.Fatalf("New should clear the webhook once")
	}
	return g, f
}

func TestSendBuildsKeyboardAndThread(t *testing.T) {
	g, f := newTestGateway(t)
	f.respond("sendMessage", `{"ok":true,"result":{"message_id":55}}`)

	id, err := g.Send(context.Background(), domain.Outgoing{
		ChatID:   -100,
		ThreadID: 12,
		Text:     "continue?",
		Notify:   true,
		Buttons: []domain.Button{
			{Text: "1 · Yes", Data: "1"},
			{Text: "2 · No", Data: "2"},
		},
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if id != 55 {
		t.Fatalf("message id = %d", id)
	}
	call := f.callsOf("sendMessage")[0]
	if call["chat_id"] != float64(-100) || call["message_thread_id"] != float64(12) {
		t.Fatalf("routing fields: %v", call)
	}
	if call["disable_notification"] != false {
		t.Fatalf("Notify must ring: %v", call)
	}
	rows := call["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	if len(rows) != 2 {
		t.Fatalf("keyboard rows = %d, want one per button", len(rows))
	}
	first := rows[0].([]any)[0].(map[string]any)
	if first["text"] != "1 · Yes" || first["callback_data"] != "1" {
		t.Fatalf("first button: %v", first)
	}
}

func TestSendGeneralIsSilentWithoutThread(t *testing.T) {
	g, f := newTestGateway(t)
	f.respond("sendMessage", `{"ok":true,"result":{"message_id":1}}`)
	if _, err := g.Send(context.Background(), domain.Outgoing{ChatID: -100, Text: "hi"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	call := f.callsOf("sendMessage")[0]
	if _, has := call["message_thread_id"]; has {
		t.Fatalf("thread must be omitted for General: %v", call)
	}
	if call["disable_notification"] != true {
		t.Fatalf("default is silent: %v", call)
	}
	if _, has := call["parse_mode"]; has {
		t.Fatalf("plain text sends no parse_mode: %v", call)
	}
}

func TestSendTruncatesToTextMax(t *testing.T) {
	g, f := newTestGateway(t)
	f.respond("sendMessage", `{"ok":true,"result":{"message_id":1}}`)
	long := strings.Repeat("é", 6000) // two UTF-16 units each
	if _, err := g.Send(context.Background(), domain.Outgoing{ChatID: -100, Text: long}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := f.callsOf("sendMessage")[0]["text"].(string)
	if n := fitUTF16([]rune(sent), textMax); n != len([]rune(sent)) || len([]rune(sent)) > textMax {
		t.Fatalf("sent text is %d runes, over the %d-unit budget", len([]rune(sent)), textMax)
	}
	if !strings.HasSuffix(sent, "…") {
		t.Fatalf("truncated text should end with an ellipsis")
	}
}

func TestEditKeyboardEmptyRemoves(t *testing.T) {
	g, f := newTestGateway(t)
	if err := g.EditKeyboard(context.Background(), -100, 9, nil); err != nil {
		t.Fatalf("edit keyboard: %v", err)
	}
	call := f.callsOf("editMessageReplyMarkup")[0]
	if call["message_id"] != float64(9) {
		t.Fatalf("wrong message: %v", call)
	}
	kb, has := call["reply_markup"].(map[string]any)
	if !has {
		t.Fatalf("reply_markup must be present even when empty: %v", call)
	}
	if rows := kb["inline_keyboard"].([]any); len(rows) != 0 {
		t.Fatalf("empty keyboard expected, got %v", rows)
	}
}
