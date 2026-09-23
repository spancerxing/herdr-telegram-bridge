// Package telegram adapts the Telegram Bot API to the ports.Telegram
// interface with nothing but the standard library: every method is one POST
// of JSON to https://api.telegram.org/bot<token>/<method>.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// APIError is a Bot API failure the caller can only log: validation and
// server errors. Rate limiting (429) is handled inside the client.
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("telegram api %d: %s (retry after %s)", e.Code, e.Description, e.RetryAfter)
	}
	return fmt.Sprintf("telegram api %d: %s", e.Code, e.Description)
}

// ErrTopicGone marks a call that failed because its forum topic no longer
// exists or is closed.
var ErrTopicGone = errors.New("telegram: topic gone")

// IsFatal reports whether an error should stop the daemon rather than be
// retried or skipped: an unauthorized token (401) or another poller
// holding getUpdates (409).
func IsFatal(err error) bool {
	var api *APIError
	return errors.As(err, &api) && (api.Code == 401 || api.Code == 409)
}

// Client talks to one bot token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	log     *slog.Logger

	// ponytail: a plain mutex instead of a paced queue — one operator and a
	// handful of agents cannot reach Telegram's per-chat limits, and a
	// 429's retry_after is the backpressure. Add a paced queue if limits bite.
	mu sync.Mutex
}

func NewClient(baseURL, token string, log *slog.Logger) *Client {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{baseURL: baseURL, token: token, http: &http.Client{}, log: log}
}

// call posts one method, serialised against every other call so messages
// leave in the order the daemon produced them.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retry(ctx, 15*time.Second, method, params, out)
}

// poll posts getUpdates outside the mutex: a 50-second long poll must not
// block every outgoing message behind it.
func (c *Client) poll(ctx context.Context, params *getUpdatesParams, out *[]update) error {
	return c.retry(ctx, 70*time.Second, "getUpdates", params, out)
}

const maxAttempts = 3

func (c *Client) retry(ctx context.Context, timeout time.Duration, method string, params, out any) error {
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = c.once(ctx, timeout, method, params, out)
		if err == nil {
			return nil
		}
		if attempt == maxAttempts || !retryable(err) {
			return err
		}
		select {
		case <-time.After(backoff(err, attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (c *Client) once(ctx context.Context, timeout time.Duration, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// net/http includes the bot token in url.Error.URL on network failure.
		var transportErr *url.Error
		if errors.As(err, &transportErr) {
			safe := *transportErr
			safe.URL = c.baseURL + "/bot[REDACTED]/" + method
			return &safe
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	var env tgResponse
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	if !env.OK {
		return classify(&env)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
	}
	return nil
}

// classify turns an error envelope into nil where Telegram is only saying
// the goal is already reached, ErrTopicGone where the topic is gone, and an
// *APIError otherwise. The descriptions were verified against a live forum
// group by the plugin this one replaces.
func classify(env *tgResponse) error {
	api := &APIError{Code: env.ErrorCode, Description: env.Description}
	if env.Parameters != nil {
		api.RetryAfter = time.Duration(env.Parameters.RetryAfter) * time.Second
	}
	lower := strings.ToLower(env.Description)
	switch {
	case strings.Contains(lower, "message is not modified"),
		strings.Contains(lower, "topic_not_modified"),
		strings.Contains(lower, "message to delete not found"),
		strings.Contains(lower, "message to edit not found"),
		strings.Contains(lower, "message to pin not found"):
		return nil
	case strings.Contains(lower, "thread not found"),
		strings.Contains(lower, "topic not found"),
		strings.Contains(lower, "topic_deleted"),
		strings.Contains(lower, "topic_id_invalid"),
		strings.Contains(lower, "topic_closed"):
		// Wrapping both keeps the sentinel usable with errors.Is and the
		// *APIError visible to retryable, so a gone topic is not retried.
		return fmt.Errorf("%w: %w", ErrTopicGone, api)
	}
	return api
}

// retryable: 429 and 5xx are worth another try, as are transport and decode
// errors; every other client-side failure is final.
func retryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Code == 429 || api.Code >= 500
	}
	return true
}

// backoff honours the retry_after a 429 carries (capped so one call cannot
// wedge the daemon) and falls back to a short fixed ladder.
func backoff(err error, attempt int) time.Duration {
	var api *APIError
	if errors.As(err, &api) && api.Code == 429 && api.RetryAfter > 0 {
		if api.RetryAfter > time.Minute {
			return time.Minute
		}
		return api.RetryAfter
	}
	return time.Duration(attempt) * time.Second
}
