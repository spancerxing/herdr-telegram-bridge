package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultSocketPath is where Herdr puts its API socket when the environment
// does not say otherwise.
func DefaultSocketPath() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

// APIError is a failure the Herdr server reported. Code is the machine
// readable part and the only thing callers should branch on.
type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return "herdr: " + e.Message
	}
	return fmt.Sprintf("herdr %s: %s", e.Code, e.Message)
}

// Is lets callers match a code with errors.Is without naming APIError.
func (e *APIError) Is(target error) bool {
	var other *APIError
	if !errors.As(target, &other) {
		return false
	}
	return other.Code == e.Code
}

// ErrDisconnected means the connection ended before a reply arrived. It is
// the one error a retry cannot duplicate, because the request may not have
// been executed.
var ErrDisconnected = errors.New("herdr: disconnected before reply")

// callSeq numbers requests across connections so a log can be correlated.
var callSeq atomic.Int64

// Client is the transport. It holds no state beyond configuration: every
// call opens its own connection, which is what Herdr supports today and what
// keeps a hung call from blocking the daemon's other work.
type Client struct {
	path        string
	log         *slog.Logger
	dialTimeout time.Duration
	// callTimeout bounds one request end to end. WaitStatus sets its own.
	callTimeout time.Duration
}

// NewClient returns a client for a socket path. An empty path uses
// DefaultSocketPath.
func NewClient(path string, log *slog.Logger) *Client {
	if path == "" {
		path = DefaultSocketPath()
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{
		path:        path,
		log:         log,
		dialTimeout: 5 * time.Second,
		callTimeout: 15 * time.Second,
	}
}

// Path is the socket this client talks to, for the doctor output.
func (c *Client) Path() string { return c.path }

// dial opens one connection.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := dialFunc(c.path)
	dctx, cancel := context.WithTimeout(ctx, c.dialTimeout)
	defer cancel()
	conn, err := d(dctx)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", c.path, err)
	}
	return conn, nil
}

// call performs one request on a fresh connection.
//
// Unlike the previous generation of this plugin, this one also works when
// the server keeps the connection open: it stops as soon as the reply with
// the matching id arrives rather than waiting for EOF.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	return c.callWith(ctx, method, params, out)
}

func (c *Client) callWith(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	id := fmt.Sprintf("r%d", callSeq.Add(1))
	line, err := json.Marshal(request{ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	start := time.Now()
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return ctxErr(ctx, method, fmt.Errorf("write %s: %w", method, err))
	}
	rd := bufio.NewReader(conn)
	for {
		raw, err := rd.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				err = fmt.Errorf("%w: server closed before replying to %s", ErrDisconnected, method)
			} else {
				err = fmt.Errorf("read %s: %w", method, err)
			}
			return ctxErr(ctx, method, err)
		}
		var r response
		if json.Unmarshal(raw, &r) != nil || r.ID != id {
			// An event line or someone else's reply. Events cannot arrive
			// here because this connection never subscribed.
			continue
		}
		c.log.Log(ctx, slog.LevelDebug, "herdr call",
			slog.String("method", method),
			slog.String("id", id),
			slog.Int64("dur_ms", time.Since(start).Milliseconds()),
			slog.String("err", errString(r.Error)))
		if r.Error != nil {
			return &APIError{Code: r.Error.Code, Message: r.Error.Message}
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(r.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	}
}

// stream opens one long-lived subscription connection and hands every event
// to onEvent until the connection ends or onEvent returns an error. The
// caller owns reconnecting; see Gateway.Subscribe.
func (c *Client) stream(ctx context.Context, subs []subscription, onStarted func(), onEvent func(eventEnvelope) error) error {
	params := eventsSubscribeParams{Subscriptions: subs}
	line, err := json.Marshal(request{ID: "sub1", Method: "events.subscribe", Params: params})
	if err != nil {
		return fmt.Errorf("encode events.subscribe: %w", err)
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if _, err := conn.Write(append(line, '\n')); err != nil {
		return ctxErr(ctx, "events.subscribe", fmt.Errorf("write: %w", err))
	}
	rd := bufio.NewReader(conn)
	acked := false
	for {
		raw, err := rd.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("%w: subscription ended", ErrDisconnected)
			}
			return ctxErr(ctx, "events", fmt.Errorf("read: %w", err))
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			continue
		}
		// The first line for a subscription is either the ack or an error;
		// after that every line is an event.
		if !acked {
			var r response
			if json.Unmarshal(raw, &r) == nil && r.ID == "sub1" {
				acked = true
				if r.Error != nil {
					return &APIError{Code: r.Error.Code, Message: r.Error.Message}
				}
				var ack subscriptionStartedResult
				if err := json.Unmarshal(r.Result, &ack); err != nil {
					return fmt.Errorf("decode subscription ack: %w", err)
				}
				c.log.Debug("herdr subscription started", slog.Int("subscriptions", len(subs)))
				if onStarted != nil {
					onStarted()
				}
				continue
			}
			// Herdr may start delivering events before the ack line. The stream is
			// live at that point even though the reply arrived out of order.
			acked = true
			if onStarted != nil {
				onStarted()
			}
		}
		var env eventEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			c.log.Warn("herdr event not understood", slog.String("line", truncate(trimmed, 200)))
			continue
		}
		if env.Event == "" {
			continue
		}
		if err := onEvent(env); err != nil {
			return err
		}
	}
}

// ctxErr prefers the context's own error once it has been cancelled, since
// the transport error is then only a symptom of closing the connection.
func ctxErr(ctx context.Context, method string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("herdr %s: %w", method, ctx.Err())
	}
	return err
}

func errString(e *wireError) string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
