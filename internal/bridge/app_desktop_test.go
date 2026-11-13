package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/discobox-ai/discobox/pkg/execstream/frame"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDesktopRelayIsPrivateAndIndependentOfTerminal(t *testing.T) {
	network := testhttp.NewNetwork(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var writes atomic.Int32
	var allowExec atomic.Bool

	viewer := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/websockify" {
			t.Errorf("unexpected viewer path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("upstream credential leaked into desktop service")
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.CloseNow()
		if err = ws.Write(ctx, websocket.MessageBinary, []byte("PRIVATE_PIXEL_BYTES")); err != nil {
			return
		}
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if string(data) != "PRIVATE_INPUT" {
				t.Errorf("wrong desktop input")
			}
			writes.Add(1)
		}
	}))
	defer viewer.Close()
	target, _ := url.Parse(viewer.URL)
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/p/sandboxes/box":
			w.Write([]byte(`{"id":"box","parentSandboxId":"caller","runtime":{"desiredState":"present","agentStatus":{"ports":[{"serviceId":"ai.discobox.desktop","protocol":"http","port":7001}]}}}`))
		case "/api/projects/p/sandboxes/box/execs":
			if !allowExec.Load() {
				t.Error("desktop resolved a primary exec")
			}
			w.Write([]byte(`{"execs":[{"id":"e","primary":true}]}`))
		case "/api/projects/p/sandboxes/box/tcp/attach":
			if r.Header.Get("Authorization") != "Bearer upstream-private" {
				t.Error("missing upstream authorization")
			}
			if r.URL.Query().Get("host") != "127.0.0.1" || r.URL.Query().Get("port") != "7001" {
				t.Error("tunnel target not pinned to declared desktop")
			}
			tcp, err := http.DefaultTransport.(*http.Transport).DialContext(ctx, "tcp", target.Host)
			if err != nil {
				t.Error(err)
				return
			}
			defer tcp.Close()
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer ws.CloseNow()
			wire := websocket.NetConn(ctx, ws, websocket.MessageBinary)
			go func() {
				for {
					f, err := frame.Read(wire)
					if err != nil {
						tcp.Close()
						return
					}
					if f.Type == frame.Input {
						if _, err = tcp.Write(f.Payload); err != nil {
							return
						}
					}
				}
			}()
			io.Copy(desktopTestOutput{wire}, tcp)
		default:
			t.Errorf("desktop depended on terminal or wrong route: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	catalog, _ := discobox.NewCatalog(backend.URL, "upstream-private", "p")
	s, _ := New(catalog, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{Parent: "caller"})
	client := mcpClient(t, ctx, s)
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s failed: %v %v", name, result, err)
		}
		return result
	}
	call("open_box_desktop", map[string]any{"boxId": "box"})
	opened := call("desktop_open", map[string]any{"boxId": "box"})
	private, _ := opened.Meta["desktop"].(map[string]any)
	id, _ := private["desktopId"].(string)
	if id == "" {
		t.Fatal("private session missing")
	}
	defer s.closeDesktop(id)
	result := call("desktop_read", map[string]any{"desktopId": id, "after": 0})
	data, _ := result.Meta["desktop"].(map[string]any)
	chunks, _ := data["chunks"].([]any)
	if len(chunks) == 0 {
		t.Fatal("no desktop bytes")
	}
	bytes, err := base64.StdEncoding.DecodeString(chunks[0].(string))
	if err != nil || string(bytes) != "PRIVATE_PIXEL_BYTES" {
		t.Fatal("wrong private pixels")
	}
	for _, r := range []*mcp.CallToolResult{opened, result} {
		visible, _ := json.Marshal(map[string]any{"content": r.Content, "structuredContent": r.StructuredContent})
		for _, secret := range []string{"PRIVATE_PIXEL", base64.StdEncoding.EncodeToString(bytes), id, "upstream-private"} {
			if strings.Contains(string(visible), secret) {
				t.Fatal("desktop bytes/handle escaped into model-visible output")
			}
		}
		if r.StructuredContent != nil {
			t.Fatal("desktop transport used structuredContent")
		}
	}
	repeated := call("desktop_read", map[string]any{"desktopId": id, "after": 0})
	encoded, _ := json.Marshal(repeated.Meta)
	original, _ := json.Marshal(result.Meta)
	if string(encoded) != string(original) {
		t.Fatal("read retry changed bytes")
	}
	call("desktop_action", map[string]any{"desktopId": id, "action": "claim"})
	manager := s.boxController("box")
	if manager.Client != nil {
		t.Fatal("desktop resolved a primary exec")
	}
	if manager.writeAs("mcp", func() error { return nil }) == nil {
		t.Fatal("assistant write bypassed desktop focus")
	}
	input := map[string]any{"desktopId": id, "action": "write", "sequence": 1, "data": base64.StdEncoding.EncodeToString([]byte("PRIVATE_INPUT"))}
	call("desktop_action", input)
	call("desktop_action", input)
	call("desktop_action", map[string]any{"desktopId": id, "action": "release"})
	if err := manager.writeAs("mcp", func() error { return nil }); err != nil {
		t.Fatal("desktop failed to yield")
	}
	allowExec.Store(true)
	connected, err := s.manager(ctx, "box")
	if err != nil || connected != manager || connected.Client.Session.Exec != "e" {
		t.Fatal("terminal connection lost the shared desktop controller", err)
	}
	call("desktop_action", map[string]any{"desktopId": id, "action": "close"})
	if writes.Load() != 1 {
		t.Fatalf("input replayed: %d", writes.Load())
	}
	if _, err := s.desktop(id); err == nil {
		t.Fatal("closed desktop remains reachable")
	}
}

type desktopTestOutput struct{ io.Writer }

func (w desktopTestOutput) Write(p []byte) (int, error) {
	if err := frame.Write(w.Writer, frame.Stdout, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func TestDesktopBackpressurePreservesBytesWithinBounds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	view := &desktopSession{conn: left, wake: make(chan struct{}), space: make(chan struct{})}
	go view.readOutput()
	payload := bytes.Repeat([]byte("private-fixture-bytes"), 220000)
	sent := make(chan error, 1)
	go func() { _, err := right.Write(payload); right.Close(); sent <- err }()
	var received []byte
	var cursor uint64
	for {
		data, err := view.read(ctx, cursor)
		if err != nil {
			t.Fatal(err)
		}
		batch := 0
		for _, chunk := range data["chunks"].([]string) {
			part, err := base64.StdEncoding.DecodeString(chunk)
			if err != nil {
				t.Fatal(err)
			}
			received = append(received, part...)
			batch += len(part)
		}
		if batch > desktopReply {
			t.Fatal("oversized desktop reply")
		}
		view.mu.Lock()
		buffered := view.bytes
		view.mu.Unlock()
		if buffered > desktopBuffer {
			t.Fatal("unbounded desktop producer")
		}
		cursor = data["cursor"].(uint64)
		if data["closed"] == true {
			break
		}
	}
	if !bytes.Equal(received, payload) {
		t.Fatal("backpressure dropped or duplicated VNC bytes")
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}
