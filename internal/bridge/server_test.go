package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeBackend struct {
	network  *testhttp.Network
	server   *httptest.Server
	inputs   atomic.Int32
	attaches atomic.Int32
	frames   chan byte
	detached chan struct{}
}

func newBackend(t *testing.T) *fakeBackend {
	t.Helper()
	b := &fakeBackend{network: testhttp.NewNetwork(t), frames: make(chan byte, 16), detached: make(chan struct{}, 16)}
	b.server = b.network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/projects/p/sandboxes/manager/execs/e/") {
			t.Errorf("escaped pinned session: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Error("upstream authorization missing")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/screen"):
			w.Write([]byte(`{"cols":80,"rows":24,"lines":["manager ready"]}`))
		case strings.HasSuffix(r.URL.Path, "/input"):
			b.inputs.Add(1)
			w.Write([]byte(`{"resumeAfter":"opaque-cursor"}`))
		case strings.HasSuffix(r.URL.Path, "/wait"):
			w.Write([]byte(`{"reason":"programState","resumeAfter":"next-cursor","programStatus":{"state":"done"}}`))
		case strings.HasSuffix(r.URL.Path, "/attach"):
			b.attaches.Add(1)
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer ws.CloseNow()
			defer func() { b.detached <- struct{}{} }()
			conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
			for {
				var header [5]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					return
				}
				n := binary.BigEndian.Uint32(header[1:])
				if n > 128<<10 {
					t.Error("oversized client frame")
					return
				}
				if _, err := io.CopyN(io.Discard, conn, int64(n)); err != nil {
					return
				}
				b.frames <- header[0]
				if header[0] == discobox.Ready {
					data := []byte("manager ready\r\n")
					frame := make([]byte, 5+len(data))
					frame[0] = discobox.Stdout
					binary.BigEndian.PutUint32(frame[1:5], uint32(len(data)))
					copy(frame[5:], data)
					if _, err := conn.Write(frame); err != nil {
						return
					}
				}
			}
		default:
			t.Errorf("unexpected endpoint: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(b.server.Close)
	return b
}

func testServer(t *testing.T, b *fakeBackend) (*Server, *httptest.Server) {
	t.Helper()
	c, err := discobox.New(b.server.URL, "upstream-secret", discobox.Session{Project: "p", Box: "manager", Exec: "e"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := b.network.NewUnstartedServer(nil)
	s, err := New(c, "http://"+httpServer.Listener.Addr().String(), nil, "", discobox.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	httpServer.Config.Handler = s.Handler(nil)
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return s, httpServer
}

func mcpClient(t *testing.T, ctx context.Context, s *Server) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestManagerToolsAndHumanHandoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b := newBackend(t)
	s, host := testServer(t, b)
	client := mcpClient(t, ctx, s)
	listed, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 27 {
		t.Fatalf("tools: %d", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if tool.Name == "open_manager" && tool.Meta["ui"] == nil {
			t.Error("missing MCP App resource metadata")
		}
	}
	call := func(name string, args map[string]any, wantError bool) *mcp.CallToolResult {
		t.Helper()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != wantError {
			t.Fatalf("%s: error=%v content=%v", name, result.IsError, result.Content)
		}
		return result
	}
	workspace := call("open_box", map[string]any{"managerId": "manager"}, false)
	encoded, _ := json.Marshal(workspace.StructuredContent)
	if !strings.Contains(string(encoded), `"view":"workspace"`) || b.attaches.Load() != 0 {
		t.Fatal("opening the workspace attached or woke the terminal")
	}
	call("inspect_box", map[string]any{"managerId": "manager"}, false)
	if b.attaches.Load() != 0 || b.inputs.Load() != 0 {
		t.Fatal("inspection attached or wrote terminal input")
	}
	call("read_manager", map[string]any{"scrollback": 20}, false)
	result := call("send_manager", map[string]any{"text": "review the patch", "submit": true}, false)
	data, _ := json.Marshal(result)
	if !strings.Contains(string(data), "opaque-cursor") || strings.Contains(string(data), s.Token()) || strings.Contains(string(data), "upstream-secret") {
		t.Fatalf("unexpected tool output: %s", data)
	}
	call("wait_manager", map[string]any{"after": "opaque-cursor", "timeoutSeconds": 1}, false)
	call("interrupt_manager", map[string]any{}, false)
	if b.inputs.Load() != 2 {
		t.Fatalf("writes: %d", b.inputs.Load())
	}

	ws, _, err := websocket.Dial(ctx, strings.Replace(host.URL, "http", "ws", 1)+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{host.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	write := func(message any) {
		t.Helper()
		if err := sendJSON(ctx, ws, message); err != nil {
			t.Fatal(err)
		}
	}
	read := func(wantType string) {
		t.Helper()
		kind, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if kind != websocket.MessageText || json.Unmarshal(data, &message) != nil || message["type"] != wantType {
			t.Fatalf("wanted %s, got %s", wantType, data)
		}
	}
	write(map[string]any{"type": "auth", "token": s.Token()})
	read("connected")
	kind, output, err := ws.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || string(output) != "manager ready\r\n" {
		t.Fatalf("terminal output: %q %v", output, err)
	}
	write(map[string]any{"type": "input", "data": "must not run"})
	read("error")
	write(map[string]any{"type": "claim"})
	read("control")
	call("send_manager", map[string]any{"text": "must not run", "submit": true}, true)
	call("interrupt_manager", map[string]any{}, true)
	call("read_manager", map[string]any{}, false)
	if b.inputs.Load() != 2 {
		t.Fatal("MCP write ran while a person had control")
	}
	write(map[string]any{"type": "input", "data": "human input"})
	write(map[string]any{"type": "resize", "cols": 100, "rows": 30})
	for _, want := range []byte{discobox.Ready, discobox.Input, discobox.Resize, discobox.Repaint} {
		select {
		case got := <-b.frames:
			if got != want {
				t.Fatalf("frame: got %d want %d", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	write(map[string]any{"type": "release"})
	read("control")
	call("send_manager", map[string]any{"text": "continue", "submit": true}, false)
	write(map[string]any{"type": "claim"})
	read("control")
	ws.CloseNow()
	select {
	case <-b.detached:
	case <-ctx.Done():
		t.Fatal("upstream did not detach")
	}
	// Cleanup releases the lease after the upstream attach has closed.
	deadline := time.Now().Add(time.Second)
	for {
		s.managers[s.Client.Session.Box].mu.Lock()
		released := s.managers[s.Client.Session.Box].controller == ""
		s.managers[s.Client.Session.Box].mu.Unlock()
		if released {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("browser disconnect retained control")
		}
		time.Sleep(time.Millisecond)
	}
	call("send_manager", map[string]any{"text": "resume after disconnect", "submit": true}, false)
}

func TestLocalAuthenticationAndOrigins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b := newBackend(t)
	s, host := testServer(t, b)
	for _, tc := range []struct {
		token, origin, host string
		status              int
	}{
		{"", "", "", 401}, {"wrong", "", "", 401}, {s.Token(), "https://untrusted.example", "", 403},
		{s.Token(), "", "rebind.example", 403}, {s.Token(), host.URL, "", 200},
	} {
		r, err := http.NewRequestWithContext(ctx, "GET", host.URL+"/api/session", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Origin", tc.origin)
		if tc.host != "" {
			r.Host = tc.host
		}
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Errorf("auth status %d want %d", response.StatusCode, tc.status)
		}
	}
	url := strings.Replace(host.URL, "http", "ws", 1) + "/terminal"
	if ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://untrusted.example"}}}); err == nil {
		ws.CloseNow()
		t.Fatal("untrusted WS origin accepted")
	}
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{host.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if err := sendJSON(ctx, ws, map[string]any{"type": "auth", "token": "wrong"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("wrong token was not refused: %v", err)
	}
	if b.attaches.Load() != 0 || b.inputs.Load() != 0 {
		t.Fatal("unauthenticated request reached manager")
	}
}
