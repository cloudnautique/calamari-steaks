package bridge

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAppTerminalDeliversNewOutputWithoutScreenRefresh(t *testing.T) {
	for _, ended := range []bool{false, true} {
		name := "transport drop"
		if ended {
			name = "session exit"
		}
		t.Run(name, func(t *testing.T) {
			network := testhttp.NewNetwork(t)
			var screens atomic.Int32
			emit := make(chan struct{})
			finish := make(chan struct{})
			backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/projects/p/sandboxes/box/execs/e/screen" {
					screens.Add(1)
					w.Write([]byte(`{"cols":80,"rows":24}`))
					return
				}
				if r.URL.Path != "/api/projects/p/sandboxes/box/execs/e/attach" {
					t.Errorf("unexpected %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				ws, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer ws.CloseNow()
				conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
				var header [5]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					return
				}
				if header[0] != discobox.Ready {
					t.Error("missing replay ready")
					return
				}
				if _, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(header[1:]))); err != nil {
					return
				}
				write := func(kind byte, text string) {
					data := make([]byte, 5+len(text))
					data[0] = kind
					binary.BigEndian.PutUint32(data[1:5], uint32(len(text)))
					copy(data[5:], text)
					conn.Write(data)
				}
				write(discobox.Stdout, "initial\r\n")
				select {
				case <-emit:
				case <-r.Context().Done():
					return
				}
				write(discobox.Stdout, "fresh output\r\n")
				select {
				case <-finish:
				case <-r.Context().Done():
					return
				}
				if ended {
					write(discobox.Exit, `{"exitCode":0}`)
				}
			}))
			defer backend.Close()
			upstream, err := discobox.New(backend.URL, "", discobox.Session{Project: "p", Box: "box", Exec: "e"})
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := New(upstream, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := mcpClient(t, ctx, bridge)
			call := func(name string, args map[string]any) map[string]any {
				t.Helper()
				result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
				if err != nil || result.IsError {
					t.Fatalf("%s: %v %v", name, result, err)
				}
				data, _ := json.Marshal(result.StructuredContent)
				var value map[string]any
				json.Unmarshal(data, &value)
				return value
			}
			id := call("terminal_open", map[string]any{})["terminalId"].(string)
			defer bridge.closeAppTerminal(id)
			first := call("terminal_read", map[string]any{"terminalId": id, "after": 0})
			type response struct {
				data map[string]any
				err  error
			}
			arrived := make(chan response, 1)
			go func() {
				result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "terminal_read", Arguments: map[string]any{"terminalId": id, "after": first["cursor"]}})
				if err != nil {
					arrived <- response{err: err}
					return
				}
				encoded, _ := json.Marshal(result.StructuredContent)
				var value map[string]any
				json.Unmarshal(encoded, &value)
				arrived <- response{data: value}
			}()
			close(emit)
			var fresh map[string]any
			select {
			case result := <-arrived:
				if result.err != nil {
					t.Fatal(result.err)
				}
				fresh = result.data
			case <-time.After(time.Second):
				t.Fatal("new output waited for a refresh or polling timeout")
			}
			chunks := fresh["chunks"].([]any)
			data, err := base64.StdEncoding.DecodeString(chunks[0].(string))
			if err != nil || string(data) != "fresh output\r\n" {
				t.Fatalf("new output: %q %v", data, err)
			}
			if screens.Load() != 1 {
				t.Fatal("stream depended on repeated screen reads")
			}
			close(finish)
			closed := call("terminal_read", map[string]any{"terminalId": id, "after": fresh["cursor"]})
			if closed["closed"] != true || closed["ended"] != ended {
				t.Fatalf("wrong reconnect classification: %v", closed)
			}
		})
	}
}
