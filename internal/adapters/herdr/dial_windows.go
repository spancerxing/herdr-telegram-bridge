//go:build windows

package herdr

import (
	"context"
	"errors"
	"net"
)

// ErrUnsupportedPlatform is returned on platforms whose Herdr socket
// transport this adapter does not implement yet.
var ErrUnsupportedPlatform = errors.New("herdr: this platform's socket transport is not implemented")

// dialFunc returns a dialer that always fails.
//
// Herdr on Windows reaches its server over a named pipe rather than a unix
// socket, and the plugin has not been run against a real Herdr there. Failing
// loudly is deliberate: a build that compiles and then silently cannot talk
// to Herdr is worse than one that says so.
func dialFunc(string) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		return nil, ErrUnsupportedPlatform
	}
}
