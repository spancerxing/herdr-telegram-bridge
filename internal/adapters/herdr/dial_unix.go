//go:build !windows

package herdr

import (
	"context"
	"net"
)

// dialFunc returns the dialer for a Herdr socket path. On unix-like systems
// this is an ordinary unix domain socket. It is a function rather than a
// direct net.Dial so tests can substitute a fake.
func dialFunc(path string) func(context.Context) (net.Conn, error) {
	var d net.Dialer
	return func(ctx context.Context) (net.Conn, error) {
		return d.DialContext(ctx, "unix", path)
	}
}
