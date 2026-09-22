package telegram

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/spancerxing/herdr-telegram-bridge/internal/domain"
	"github.com/spancerxing/herdr-telegram-bridge/internal/ports"
)

// recordingHandler captures what Stream dispatches.
type recordingHandler struct {
	mu    sync.Mutex
	got   []string
	sent  chan string
}

func (h *recordingHandler) record(s string) {
	h.mu.Lock()
	h.got = append(h.got, s)
	h.mu.Unlock()
	select {
	case h.sent <- s:
	default:
	}
}

func (h *recordingHandler) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.got...)
}

func (h *recordingHandler) wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		h.mu.Lock()
		got := len(h.got)
		h.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("dispatch stalled at %d of %d: %v", got, n, h.snapshot())
		case <-h.sent:
		}
	}
}

func (h *recordingHandler) TopicMessage(_ context.Context, m domain.TopicMessage) {
	h.record("topic-message " + m.Text)
}
func (h *recordingHandler) GeneralMessage(_ context.Context, m domain.TopicMessage) {
	h.record("general-message " + m.Text)
}
func (h *recordingHandler) Button(_ context.Context, b domain.ButtonPress) {
	h.record("button " + b.Data + " from " + strconv.FormatInt(b.FromID, 10))
}
func (h *recordingHandler) TopicEdited(_ context.Context, e domain.TopicEdit) {
	h.record("topic-edited " + e.Name)
}
func (h *recordingHandler) BotRightsChanged(_ context.Context, r domain.BotRights) {
	h.record("rights " + r.Status)
}

var _ ports.UpdateHandler = (*recordingHandler)(nil)

func TestStreamDispatchesAndFilters(t *testing.T) {
	origDelay := noticeDelay
	noticeDelay = 20 * time.Millisecond
	t.Cleanup(func() { noticeDelay = origDelay })

	g, f := newTestGateway(t)
	f.respond("getUpdates", `{"ok":true,"result":[
		{"update_id":1,"callback_query":{"id":"cb1","from":{"id":666},"message":{"message_id":10,"chat":{"id":-100}},"data":"1"}},
		{"update_id":2,"callback_query":{"id":"cb2","from":{"id":7},"message":{"message_id":11,"chat":{"id":-100},"message_thread_id":3},"data":"2"}},
		{"update_id":3,"message":{"message_id":12,"from":{"id":7},"chat":{"id":-100},"message_thread_id":3,"text":"hello agent"}},
		{"update_id":4,"message":{"message_id":13,"from":{"id":7},"chat":{"id":-100},"forum_topic_edited":{"name":"renamed"}}},
		{"update_id":5,"message":{"message_id":14,"from":{"id":55},"chat":{"id":-555},"text":"another group"}},
		{"update_id":6,"message":{"message_id":15,"from":{"id":42},"chat":{"id":-100},"forum_topic_closed":{}}}
	]}`)

	h := &recordingHandler{sent: make(chan string, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = g.Stream(ctx, h) }()

	h.wait(t, 3) // the stranger's press, the other group and the bot's own notice are not dispatched
	got := h.snapshot()
	want := []string{
		"button 2 from 7",
		"topic-message hello agent",
		"topic-edited renamed",
	}
	for _, w := range want {
		found := false
		for _, s := range got {
			if s == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing dispatch %q in %v", w, got)
		}
	}
	for _, unwanted := range []string{"button 1", "another group", "general-message"} {
		for _, s := range got {
			if s == unwanted || (unwanted == "general-message" && len(s) > 0 && s[0] == 'g') {
				t.Errorf("unexpected dispatch %q", s)
			}
		}
	}

	// The stranger's press is answered and dropped.
	calls := f.callsOf("answerCallbackQuery")
	if len(calls) == 0 || calls[0]["text"] != "not allowed" {
		t.Fatalf("stranger not answered: %v", calls)
	}

	// Polling resumes past the batch: the second getUpdates carries the
	// advanced offset.
	waitFor(t, func() bool { return len(f.callsOf("getUpdates")) >= 2 })
	second := f.callsOf("getUpdates")[1]
	if second["offset"] != float64(7) {
		t.Fatalf("offset after update 6 = %v, want 7", second["offset"])
	}

	// The bot's own topic-closed notice is deleted after the notice delay.
	waitFor(t, func() bool { return f.count("deleteMessage") >= 1 })
	del := f.callsOf("deleteMessage")[0]
	if del["message_id"] != float64(15) {
		t.Fatalf("deleted the wrong notice: %v", del)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal("condition never became true")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestStreamFatalStops(t *testing.T) {
	f := newFakeAPI(t)
	c := NewClient(f.server.URL, "T", nil)
	g := &Gateway{cli: c, cfg: Config{ChatID: -100}, log: nil}
	f.respond("getUpdates", `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`)

	err := g.Stream(context.Background(), &recordingHandler{})
	if err == nil || !IsFatal(err) {
		t.Fatalf("want a fatal error, got %v", err)
	}
}

func TestAwaitChatSeesGroupMessage(t *testing.T) {
	g, f := newTestGateway(t)
	f.respond("getUpdates", `{"ok":true,"result":[
		{"update_id":1,"message":{"message_id":1,"from":{"id":7},"chat":{"id":111},"text":"private first"}},
		{"update_id":2,"message":{"message_id":2,"from":{"id":7},"chat":{"id":-100200},"message_thread_id":3,"text":"in the group"}}
	]}`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chat, from, err := g.AwaitChat(ctx)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if chat != -100200 || from != 7 {
		t.Fatalf("chat=%d from=%d", chat, from)
	}
}

func TestAwaitChatSeesPromotion(t *testing.T) {
	g, f := newTestGateway(t)
	f.respond("getUpdates", `{"ok":true,"result":[
		{"update_id":1,"my_chat_member":{"chat":{"id":-100300},"from":{"id":7},"new_chat_member":{"status":"administrator","can_manage_topics":true}}}
	]}`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chat, from, err := g.AwaitChat(ctx)
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if chat != -100300 || from != 7 {
		t.Fatalf("chat=%d from=%d", chat, from)
	}
}
