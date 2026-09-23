package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClientUsesDefaultTransport(t *testing.T) {
	c := NewClient("", "T", nil)
	if c.http.Transport != nil {
		t.Fatal("client must use the standard HTTP transport, not custom proxy routing")
	}
}

func TestClientDecodesResultAndHidesToken(t *testing.T) {
	f := newFakeAPI(t)
	f.respond("getMe", `{"ok":true,"result":{"id":42,"is_bot":true}}`)
	c := NewClient(f.server.URL, "sekret", nil)

	var me userResult
	if err := c.call(context.Background(), "getMe", struct{}{}, &me); err != nil {
		t.Fatalf("call: %v", err)
	}
	if me.ID != 42 || !me.IsBot {
		t.Fatalf("decoded %+v", me)
	}
	// The token belongs in the URL path only.
	if got := f.count("getMe"); got != 1 {
		t.Fatalf("calls = %d", got)
	}
}

func TestClientRetries429ThenSucceeds(t *testing.T) {
	f := newFakeAPI(t)
	f.respond("sendMessage",
		`{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":0}}`,
		`{"ok":true,"result":{"message_id":7}}`)
	c := NewClient(f.server.URL, "T", nil)

	var res messageIDResult
	if err := c.call(context.Background(), "sendMessage", struct{}{}, &res); err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.MessageID != 7 {
		t.Fatalf("message_id = %d", res.MessageID)
	}
	if got := f.count("sendMessage"); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestClientGivesUpAfterThreeTries(t *testing.T) {
	f := newFakeAPI(t)
	for i := 0; i < 5; i++ {
		f.respond("sendMessage", `{"ok":false,"error_code":500,"description":"Internal Server Error"}`)
	}
	c := NewClient(f.server.URL, "T", nil)
	if err := c.call(context.Background(), "sendMessage", struct{}{}, nil); err == nil {
		t.Fatal("want an error after exhausting retries")
	}
	if got := f.count("sendMessage"); got != maxAttempts {
		t.Fatalf("attempts = %d, want %d", got, maxAttempts)
	}
}

func TestClientDoesNotRetry400(t *testing.T) {
	f := newFakeAPI(t)
	f.respond("sendMessage", `{"ok":false,"error_code":400,"description":"Bad Request: message is empty"}`)
	c := NewClient(f.server.URL, "T", nil)
	if err := c.call(context.Background(), "sendMessage", struct{}{}, nil); err == nil {
		t.Fatal("want an error")
	}
	if got := f.count("sendMessage"); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestClientNotModifiedIsSuccess(t *testing.T) {
	for _, desc := range []string{
		"Bad Request: message is not modified",
		"Bad Request: TOPIC_NOT_MODIFIED",
		"Bad Request: message to edit not found",
	} {
		f := newFakeAPI(t)
		f.respond("editMessageReplyMarkup", `{"ok":false,"error_code":400,"description":"`+desc+`"}`)
		c := NewClient(f.server.URL, "T", nil)
		if err := c.call(context.Background(), "editMessageReplyMarkup", struct{}{}, nil); err != nil {
			t.Errorf("%s: want success, got %v", desc, err)
		}
	}
}

func TestClientTopicGone(t *testing.T) {
	for _, desc := range []string{
		"Bad Request: TOPIC_ID_INVALID",
		"Bad Request: thread not found",
		"Bad Request: topic_closed",
	} {
		f := newFakeAPI(t)
		f.respond("editForumTopic", `{"ok":false,"error_code":400,"description":"`+desc+`"}`)
		c := NewClient(f.server.URL, "T", nil)
		err := c.call(context.Background(), "editForumTopic", struct{}{}, nil)
		if !errors.Is(err, ErrTopicGone) {
			t.Errorf("%s: want ErrTopicGone, got %v", desc, err)
		}
	}
}

func TestClientFatal401(t *testing.T) {
	f := newFakeAPI(t)
	f.respond("getUpdates", `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	c := NewClient(f.server.URL, "T", nil)
	err := c.call(context.Background(), "getUpdates", struct{}{}, nil)
	if !IsFatal(err) {
		t.Fatalf("401 should be fatal, got %v", err)
	}
}

func TestBackoffHonoursRetryAfter(t *testing.T) {
	err := &APIError{Code: 429, RetryAfter: 90 * time.Second}
	if got := backoff(err, 1); got != time.Minute {
		t.Fatalf("capped backoff = %v, want 1m", got)
	}
	err = &APIError{Code: 429, RetryAfter: 2 * time.Second}
	if got := backoff(err, 1); got != 2*time.Second {
		t.Fatalf("backoff = %v, want 2s", got)
	}
	if got := backoff(&APIError{Code: 500}, 2); got != 2*time.Second {
		t.Fatalf("ladder backoff = %v, want 2s", got)
	}
}
