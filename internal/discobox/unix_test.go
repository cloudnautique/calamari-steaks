//go:build !windows

package discobox

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestUnixTransportIgnoresProxyAndCarriesUpgrade(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	path := filepath.Join(t.TempDir(), "server.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/projects/p/sandboxes/b/execs/e/screen" {
			w.Write([]byte(`{"lines":["ready"]}`))
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.CloseNow()
		if err := ws.Write(ctx, websocket.MessageBinary, []byte{Stdout, 0, 0, 0, 2, 'o', 'k'}); err != nil {
			t.Error(err)
			return
		}
		ws.Read(ctx)
	})}
	go server.Serve(listener)
	defer server.Close()
	client, err := New("unix://"+path, "", Session{Project: "p", Box: "b", Exec: "e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Screen(ctx, 0); err != nil {
		t.Fatal(err)
	}
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	kind, data, err := stream.Read()
	if err != nil || kind != Stdout || string(data) != "ok" {
		t.Fatalf("read: %d %q %v", kind, data, err)
	}
}
