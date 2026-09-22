package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
)

func TestCreateTopicParams(t *testing.T) {
	g, f := newTestGateway(t)
	f.respond("createForumTopic", `{"ok":true,"result":{"message_thread_id":31}}`)

	thread, err := g.CreateTopic(context.Background(), -100, "π - task", domain.TopicIcon{CustomEmojiID: "5"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if thread != 31 {
		t.Fatalf("thread = %d", thread)
	}
	call := f.callsOf("createForumTopic")[0]
	if call["name"] != "π - task" || call["icon_custom_emoji_id"] != "5" {
		t.Fatalf("create params: %v", call)
	}

	// A long name is cut to Telegram's limit; a zero icon is omitted.
	long := strings.Repeat("n", 300)
	f.respond("createForumTopic", `{"ok":true,"result":{"message_thread_id":32}}`)
	if _, err := g.CreateTopic(context.Background(), -100, long, domain.TopicIcon{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	call = f.callsOf("createForumTopic")[1]
	if len(call["name"].(string)) != topicNameMax {
		t.Fatalf("name not cut to %d: %v", topicNameMax, call)
	}
	if _, has := call["icon_custom_emoji_id"]; has {
		t.Fatalf("zero icon must be omitted: %v", call)
	}
}

func TestEditTopicIconOnly(t *testing.T) {
	g, f := newTestGateway(t)
	if err := g.EditTopic(context.Background(), -100, 31, "", domain.TopicIcon{CustomEmojiID: "6"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	call := f.callsOf("editForumTopic")[0]
	if _, has := call["name"]; has {
		t.Fatalf("icon-only edit must not rename: %v", call)
	}
	if call["icon_custom_emoji_id"] != "6" {
		t.Fatalf("icon missing: %v", call)
	}
}

func TestEditTopicEmptyPatchSkipsCall(t *testing.T) {
	g, f := newTestGateway(t)
	if err := g.EditTopic(context.Background(), -100, 31, "", domain.TopicIcon{}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := f.count("editForumTopic"); got != 0 {
		t.Fatalf("empty patch should not call the API, got %d calls", got)
	}
}

func TestCloseAndReopenTopic(t *testing.T) {
	g, f := newTestGateway(t)
	if err := g.CloseTopic(context.Background(), -100, 31); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := g.ReopenTopic(context.Background(), -100, 31); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := f.callsOf("closeForumTopic")[0]["message_thread_id"]; got != float64(31) {
		t.Fatalf("close params: %v", got)
	}
	if got := f.callsOf("reopenForumTopic")[0]["message_thread_id"]; got != float64(31) {
		t.Fatalf("reopen params: %v", got)
	}
}

func TestDeleteTopic(t *testing.T) {
	g, f := newTestGateway(t)
	if err := g.DeleteTopic(context.Background(), -100, 31); err != nil {
		t.Fatalf("delete topic: %v", err)
	}
	calls := f.callsOf("deleteForumTopic")
	if len(calls) != 1 || calls[0]["message_thread_id"] != float64(31) || calls[0]["chat_id"] != float64(-100) {
		t.Fatalf("deleteForumTopic params: %v", calls)
	}
}

func TestTopicGoneSurfacesFromEdit(t *testing.T) {
	g, f := newTestGateway(t)
	f.respond("editForumTopic", `{"ok":false,"error_code":400,"description":"Bad Request: TOPIC_ID_INVALID"}`)
	err := g.EditTopic(context.Background(), -100, 31, "", domain.TopicIcon{CustomEmojiID: "6"})
	if !errors.Is(err, ErrTopicGone) {
		t.Fatalf("want ErrTopicGone, got %v", err)
	}
}
