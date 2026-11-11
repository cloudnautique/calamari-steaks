// Package testhttp runs real HTTP and WebSocket handlers over net.Pipe.
// Tests can exercise upgrades and streaming without opening listening sockets.
package testhttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type Network struct {
	mu        sync.Mutex
	listeners map[string]*listener
}

// NewNetwork temporarily installs a pipe dialer on the default HTTP transport.
// Call only from nonparallel tests, before constructing clients or handlers.
func NewNetwork(t *testing.T) *Network {
	t.Helper()
	n := &Network{listeners: make(map[string]*listener)}
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = n.dial
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previous })
	return n
}

func (n *Network) NewServer(handler http.Handler) *httptest.Server {
	s := n.NewUnstartedServer(handler)
	s.Start()
	return s
}

func (n *Network) NewUnstartedServer(handler http.Handler) *httptest.Server {
	n.mu.Lock()
	defer n.mu.Unlock()
	address := fmt.Sprintf("127.0.0.1:%d", 10000+len(n.listeners))
	l := &listener{address: address, incoming: make(chan net.Conn), closed: make(chan struct{})}
	n.listeners[address] = l
	return &httptest.Server{Listener: l, Config: &http.Server{Handler: handler}}
}

func (n *Network) dial(ctx context.Context, _, address string) (net.Conn, error) {
	n.mu.Lock()
	l := n.listeners[address]
	n.mu.Unlock()
	if l == nil {
		return nil, fmt.Errorf("test HTTP target not registered: %s", address)
	}
	client, server := net.Pipe()
	select {
	case l.incoming <- server:
		return client, nil
	case <-l.closed:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}

type listener struct {
	address  string
	incoming chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *listener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *listener) Addr() net.Addr { return pipeAddress(l.address) }

type pipeAddress string

func (a pipeAddress) Network() string { return "tcp" }
func (a pipeAddress) String() string  { return string(a) }
