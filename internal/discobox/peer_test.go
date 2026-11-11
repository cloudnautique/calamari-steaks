package discobox

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/discobox-ai/discobox/pkg/endpoint"
)

func TestPeerIdentityPersistsAndRefusesCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity", "key.pem")
	var group sync.WaitGroup
	ids := make(chan endpoint.IrohID, 8)
	for range 8 {
		group.Go(func() {
			_, id, err := PeerIdentity(path)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		})
	}
	group.Wait()
	close(ids)
	_, want, err := PeerIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	for id := range ids {
		if id != want {
			t.Fatal("concurrent initialization replaced the enrollment identity")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("identity permissions: %o", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PeerIdentity(path); err == nil {
		t.Fatal("corrupt identity accepted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken" {
		t.Fatal("corrupt identity replaced")
	}
}

func TestPeerEndpointRequiresPinnedIdentity(t *testing.T) {
	id := endpoint.IrohID{1}
	base := endpoint.IrohURL(id)
	for _, raw := range []string{
		"discobox://", "discobox://example.com", "discobox://127.0.0.1:80",
		base + "/box", base + "?token=secret", base + "?addr=host:123",
		base + "?addr=127.0.0.1:0", base + "?addr=%zz", base + "#fragment", base + "?",
	} {
		if _, err := New(raw, "", Session{Project: "p", Box: "b", Exec: "e"}); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := peerEndpoint(base + "?addr=127.0.0.1:1234&addr=[::1]:1234"); err != nil {
		t.Fatal(err)
	}
}

func TestPeerCarriesAPIAndTerminalWithEnrolledIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	identity := filepath.Join(t.TempDir(), "key.pem")
	_, enrolled, err := PeerIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	_, serverKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, err := endpoint.NewIrohEndpoint(endpoint.IrohConfig{
		SecretKey: serverKey, DisableRelay: true, DisableDiscovery: true,
		BindAddrs: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:0")},
		Authorize: func(_ context.Context, id endpoint.IrohID) error {
			if id != enrolled {
				return errors.New("peer not enrolled")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, _, stop, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	addrs, err := server.DirectAddrs()
	if err != nil {
		t.Fatal(err)
	}
	serverID, err := server.ID()
	if err != nil {
		t.Fatal(err)
	}
	raw := endpoint.IrohURLWithAddrs(serverID, addrs)
	if _, err := ConfigurePeer(raw, identity, nil); err != nil {
		t.Fatal(err)
	}
	const root = "/api/projects/p/sandboxes/b/execs/e"
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream" {
			t.Error("missing authorization")
		}
		switch r.URL.Path {
		case root + "/screen":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"lines":["ready"]}`))
		case root + "/attach":
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer ws.CloseNow()
			kind, data, err := ws.Read(ctx)
			if err != nil || kind != websocket.MessageBinary || !bytes.Equal(data, []byte{Ready, 0, 0, 0, 0}) {
				t.Errorf("terminal ready: %v %v", data, err)
				return
			}
			if err := ws.Write(ctx, websocket.MessageBinary, []byte{Stdout, 0, 0, 0, 2, 'o', 'k'}); err != nil {
				t.Error(err)
				return
			}
			ws.Read(ctx)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})}
	go httpServer.Serve(listener)
	defer httpServer.Close()
	client, err := New(raw, "upstream", Session{Project: "p", Box: "b", Exec: "e"})
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
	if err := stream.Write(Ready, nil); err != nil {
		t.Fatal(err)
	}
	kind, data, err := stream.Read()
	if err != nil || kind != Stdout || string(data) != "ok" {
		t.Fatalf("terminal read: %d %q %v", kind, data, err)
	}
}
