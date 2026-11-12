package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAppAttachWaitsForColdStartAndKeepsStreamAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := testhttp.NewNetwork(t)
		backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/projects/p/sandboxes/manager/execs/e/screen" {
				w.Write([]byte(`{"cols":80,"rows":24}`))
				return
			}
			// The old 15-second deadline canceled this otherwise healthy cold start.
			time.Sleep(29 * time.Second)
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
				t.Errorf("missing replay handshake: %d", header[0])
				return
			}
			if _, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(header[1:]))); err != nil {
				return
			}
			// Output arriving after the startup budget must still be delivered: the
			// budget applies to establishment, never to the live terminal lifetime.
			time.Sleep(50 * time.Second)
			conn.Write([]byte{discobox.Stdout, 0, 0, 0, 2, 'o', 'k'})
			io.Copy(io.Discard, conn)
		}))
		defer backend.Close()
		c, err := discobox.New(backend.URL, "", discobox.Session{Project: "p", Box: "manager", Exec: "e"})
		if err != nil {
			t.Fatal(err)
		}
		server, err := New(c, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		client := mcpClient(t, ctx, server)
		started := time.Now()
		opened, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "terminal_open", Arguments: map[string]any{}})
		if err != nil || opened.IsError {
			t.Fatalf("cold start rejected: %v %#v", err, opened)
		}
		if elapsed := time.Since(started); elapsed < 29*time.Second {
			t.Fatalf("test did not exercise startup wait: %s", elapsed)
		}
		data := opened.StructuredContent.(map[string]any)
		id := data["terminalId"].(string)
		defer server.closeAppTerminal(id)
		time.Sleep(50 * time.Second)
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "terminal_read", Arguments: map[string]any{"terminalId": id, "after": 0}})
		if err != nil || result.IsError {
			t.Fatalf("startup deadline killed established stream: %v %#v", err, result)
		}
		var output struct {
			Chunks []string `json:"chunks"`
			Closed bool     `json:"closed"`
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		if err := json.Unmarshal(encoded, &output); err != nil {
			t.Fatal(err)
		}
		if output.Closed || len(output.Chunks) != 1 || output.Chunks[0] != "b2s=" {
			t.Fatalf("live stream lost late output: %+v", output)
		}
	})
}

func TestAppAttachTimeoutKeepsNoViewAndReportsStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		network := testhttp.NewNetwork(t)
		backend := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/projects/p/sandboxes/manager/execs/e/screen" {
				w.Write([]byte(`{"cols":80,"rows":24}`))
				return
			}
			<-r.Context().Done()
		}))
		defer backend.Close()
		c, err := discobox.New(backend.URL, "", discobox.Session{Project: "p", Box: "manager", Exec: "e"})
		if err != nil {
			t.Fatal(err)
		}
		server, err := New(c, "http://127.0.0.1:8080", nil, "", discobox.ManagerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		client := mcpClient(t, ctx, server)
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "terminal_open", Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result.Content)
		if !result.IsError || !strings.Contains(string(encoded), "45s") || !strings.Contains(string(encoded), "no input was sent") {
			t.Fatalf("missing cold-start diagnosis: %s", encoded)
		}
		if len(server.appTerminals) != 0 {
			t.Fatal("timed-out attachment retained a terminal view")
		}
	})
}
