package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

func serviceFixture(t *testing.T, deny bool, target ...string) (*Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	network := testhttp.NewNetwork(t)
	tunnels, mutations := new(atomic.Int32), new(atomic.Int32)
	backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/p/sandboxes/box":
			json.NewEncoder(w).Encode(map[string]any{"id": "box", "projectId": "p", "runtime": map[string]any{"desiredState": "present", "agentStatus": map[string]any{"ports": []any{map[string]any{"port": 8080, "protocol": "http", "serviceId": "web.app", "serviceName": "Fixture web", "addresses": []string{"127.0.0.1"}}, map[string]any{"port": 5353, "protocol": "udp"}}}}})
		case "/api/projects/p/sandboxes/box/services":
			w.Write([]byte(`{"services":[{"id":"web.app","name":"Fixture web","status":"running","ports":[8080]}]}`))
		case "/api/projects/p/sandboxes/box/services/web.app/logs":
			w.Write([]byte(`{"entries":[{"data":"UFJJVkFURV9MT0dfU0VOVElORUw="}]}`))
		case "/api/projects/p/sandboxes/box/services/web.app/start":
			if r.Method != "POST" {
				t.Error("wrong lifecycle method")
			}
			mutations.Add(1)
			w.Write([]byte(`{"id":"web.app","status":"running"}`))
		case "/api/projects/p/sandboxes/box/tcp/attach":
			tunnels.Add(1)
			if deny {
				http.Error(w, "denied", 403)
				return
			}
			if r.URL.Query().Get("port") != "8080" || r.URL.Query().Get("host") != "localhost" {
				t.Error("unvalidated destination")
			}
			if r.Header.Get("Authorization") != "Bearer upstream-key" {
				t.Error("missing outer API auth")
			}
			ws, e := websocket.Accept(w, r, nil)
			if e != nil {
				return
			}
			defer ws.CloseNow()
			conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
			stream := &fixtureFrames{conn: conn}
			if len(target) > 0 {
				remote, e := net.Dial("tcp", target[0])
				if e != nil {
					return
				}
				defer remote.Close()
				done := make(chan struct{}, 1)
				go func() {
					defer func() { done <- struct{}{} }()
					for {
						kind, data, e := stream.Read()
						if e != nil {
							return
						}
						if kind == 7 {
							remote.(*net.TCPConn).CloseWrite()
							return
						}
						if kind != discobox.Input {
							return
						}
						if _, e = remote.Write(data); e != nil {
							return
						}
					}
				}()
				buffer := make([]byte, 32768)
				for {
					n, e := remote.Read(buffer)
					if n > 0 {
						if stream.Write(discobox.Stdout, buffer[:n]) != nil {
							return
						}
					}
					if e != nil {
						stream.Write(13, nil)
						return
					}
				}
			}

			for {
				kind, data, e := stream.Read()
				if e != nil {
					return
				}
				if kind == 7 {
					stream.Write(discobox.Stdout, []byte("AFTER_HALF_CLOSE"))
					stream.Write(13, nil)
					return
				}
				if kind != discobox.Input {
					t.Errorf("unexpected tunnel frame %d", kind)
					return
				}
				if strings.Contains(string(data), "upstream-key") {
					t.Error("upstream auth leaked into service bytes")
				}
				if e = stream.Write(discobox.Stdout, data); e != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	client, e := discobox.NewCatalog(backend.URL, "upstream-key", "p")
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(client, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.CloseServiceForwards)
	return s, tunnels, mutations
}
func TestForwardByteTrafficHalfCloseAndStableBinding(t *testing.T) {
	s, tunnels, mutations := serviceFixture(t, false)
	ctx := context.Background()
	info, e := s.openForward(ctx, serviceInput{BoxID: "box", Port: 8080})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.openForward(ctx, serviceInput{BoxID: "box", Port: 8080})
	if e != nil || info.ID != again.ID || info.Address != again.Address {
		t.Fatal("binding was not reused", e)
	}
	conn, e := net.Dial("tcp", info.Address)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("hello"))
	data := make([]byte, 5)
	if _, e = io.ReadFull(conn, data); e != nil || string(data) != "hello" {
		t.Fatalf("byte transfer %q %v", data, e)
	}
	conn.(*net.TCPConn).CloseWrite()
	tail, e := io.ReadAll(conn)
	if e != nil || string(tail) != "AFTER_HALF_CLOSE" {
		t.Fatalf("half-close lost response %q %v", tail, e)
	}
	if mutations.Load() != 0 || tunnels.Load() < 3 {
		t.Fatal("forward mutated service or did not revalidate connections")
	}
	client := mcpClient(t, ctx, s)
	for _, name := range []string{"service_keepalive", "close_service_forward"} {
		result, e := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"forwardId": info.ID}})
		if e != nil || result.IsError {
			t.Fatalf("%s rejected forward-only arguments: %+v %v", name, result, e)
		}
	}
	if len(s.forwards("box")) != 0 {
		t.Fatal("forward survived close")
	}
	if c, e := net.DialTimeout("tcp", info.Address, time.Second); e == nil {
		c.Close()
		t.Fatal("listener survived close")
	}
}
func TestForwardRequiresReportedTCPAndAuthorizedConnection(t *testing.T) {
	s, tunnels, _ := serviceFixture(t, false)
	for _, port := range []int{0, 9000, 5353} {
		if _, e := s.openForward(context.Background(), serviceInput{BoxID: "box", Port: port}); e == nil {
			t.Fatalf("opened invalid/unreported/UDP %d", port)
		}
	}
	if tunnels.Load() != 0 {
		t.Fatal("dialed invalid target")
	}
	s.managerOptions.Parent = "caller"
	if _, e := s.openForward(context.Background(), serviceInput{BoxID: "box", Port: 8080}); e == nil || !strings.Contains(e.Error(), "sandbox API role") {
		t.Fatal("sandbox role not rejected", e)
	}
	denied, _, _ := serviceFixture(t, true)
	if _, e := denied.openForward(context.Background(), serviceInput{BoxID: "box", Port: 8080}); e == nil || !strings.Contains(e.Error(), "403") {
		t.Fatal("lost API denial", e)
	}
	if len(denied.forwards("")) != 0 {
		t.Fatal("failed probe allocated a listener")
	}
}
func TestPrivateServiceLogsAndExplicitLifecycle(t *testing.T) {
	s, _, mutations := serviceFixture(t, false)
	ctx := context.Background()
	client := mcpClient(t, ctx, s)
	result, e := client.CallTool(ctx, &mcp.CallToolParams{Name: "service_logs", Arguments: map[string]any{"boxId": "box", "serviceId": "web.app"}})
	if e != nil || result.IsError {
		t.Fatal(e, result)
	}
	content, _ := json.Marshal(result.Content)
	if strings.Contains(string(content), "PRIVATE_LOG_SENTINEL") || result.StructuredContent != nil || result.Meta["services"] == nil {
		t.Fatal("private logs leaked to model output")
	}
	public, e := client.CallTool(ctx, &mcp.CallToolParams{Name: "read_service_logs", Arguments: map[string]any{"boxId": "box", "serviceId": "web.app"}})
	if e != nil || public.IsError {
		t.Fatal(e, public)
	}
	if _, e = s.Client.ControlService(ctx, "box", "", "../other", "start"); e == nil {
		t.Fatal("accepted path traversal")
	}
	if _, e = s.Client.ControlService(ctx, "box", "", "web.app", "start"); e != nil {
		t.Fatal(e)
	}
	if mutations.Load() != 1 {
		t.Fatal("lifecycle retried")
	}
}

type fixtureFrames struct{ conn net.Conn }

func (f *fixtureFrames) Read() (byte, []byte, error) {
	var h [5]byte
	if _, e := io.ReadFull(f.conn, h[:]); e != nil {
		return 0, nil, e
	}
	size := binary.BigEndian.Uint32(h[1:])
	if size > 65536 {
		return 0, nil, io.ErrShortBuffer
	}
	data := make([]byte, size)
	_, e := io.ReadFull(f.conn, data)
	return h[0], data, e
}
func (f *fixtureFrames) Write(kind byte, data []byte) error {
	b := make([]byte, 5+len(data))
	b[0] = kind
	binary.BigEndian.PutUint32(b[1:5], uint32(len(data)))
	copy(b[5:], data)
	_, e := f.conn.Write(b)
	return e
}

func TestForwardHTTPStreamingAndWebSocket(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("outer credentials reached target")
		}
		if r.URL.Path == "/ws" {
			ws, e := websocket.Accept(w, r, nil)
			if e != nil {
				return
			}
			defer ws.CloseNow()
			kind, data, e := ws.Read(r.Context())
			if e == nil {
				ws.Write(r.Context(), kind, data)
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: second\n\n")
	}))
	defer inner.Close()
	s, _, _ := serviceFixture(t, false, inner.Listener.Addr().String())
	info, e := s.openForward(context.Background(), serviceInput{BoxID: "box", Port: 8080})
	if e != nil {
		t.Fatal(e)
	}
	dialer := &net.Dialer{}
	httpClient := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}, Timeout: 5 * time.Second}
	defer httpClient.CloseIdleConnections()
	response, e := httpClient.Get(info.URL)
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || string(data) != "data: first\n\ndata: second\n\n" {
		t.Fatalf("stream failed %q %v", data, e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, e := websocket.Dial(ctx, "ws://"+info.Address+"/ws", &websocket.DialOptions{HTTPClient: httpClient})
	if e != nil {
		t.Fatal(e)
	}
	defer ws.CloseNow()
	ws.Write(ctx, websocket.MessageText, []byte("fixture socket"))
	_, data, e = ws.Read(ctx)
	if e != nil || string(data) != "fixture socket" {
		t.Fatalf("WebSocket failed %q %v", data, e)
	}
}
