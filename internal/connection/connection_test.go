package connection

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
)

func TestLocalResolverAndValidation(t *testing.T) {
	t.Setenv("DISCOBOX_SERVER_ADDRESS", "unix:///tmp/discobox%20test.sock")
	if Resolve("unix://") != "unix:///tmp/discobox%20test.sock" {
		t.Fatal("did not use upstream override")
	}
	if Validate(Config{Endpoint: "unix://", Project: "default"}) != nil {
		t.Fatal("default socket rejected")
	}
	for _, endpoint := range []string{"file:///etc/passwd", "unix://host/socket", "unix://relative", "https://user:secret@example.com", "https://host/path", "https://host?token=secret", "discobox://example.com", "unix:relative"} {
		if Validate(Config{Endpoint: endpoint, Project: "default"}) == nil {
			t.Errorf("accepted invalid %s", endpoint)
		}
	}
	if Validate(Config{Endpoint: "unix://", Project: "../other"}) == nil {
		t.Fatal("invalid project accepted")
	}
	if Validate(Config{Endpoint: "unix://", Project: "default", Relays: []string{"https://relay.example"}}) == nil {
		t.Fatal("local relay accepted")
	}
}
func TestSaveIsPrivateOfflineAndRequiresRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "connection.json")
	var calls atomic.Int32
	c := New(Config{Endpoint: "unix://", Project: "default"}, path, filepath.Join(t.TempDir(), "key"), false, func(context.Context, Config) Result { calls.Add(1); return Result{State: "connected", Message: "ok"} }, nil)
	candidate := Config{Endpoint: "unix:///tmp/other.sock", Project: "research"}
	status, e := c.Save(candidate)
	if e != nil {
		t.Fatal(e)
	}
	if !status.RestartRequired || status.State != "restart_required" || calls.Load() != 0 {
		t.Fatalf("save connected or omitted restart: %+v", status)
	}
	if c.Current.Endpoint != "unix://" {
		t.Fatal("retargeted active connection")
	}
	loaded, e := Load(path)
	if e != nil || loaded.Endpoint != candidate.Endpoint {
		t.Fatalf("profile not persisted: %+v %v", loaded, e)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("profile permissions %v", info.Mode())
	}
	if _, e = c.Save(Config{Endpoint: "https://example.com", Project: "default"}); e == nil {
		t.Fatal("widget saved a general HTTP target")
	}
	if _, e = c.Save(c.Current); e != nil || c.Status().RestartRequired {
		t.Fatal("could not restore current profile")
	}
}
func TestManagedConnectionAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	c := New(Config{Endpoint: "unix://", Project: "default"}, filepath.Join(t.TempDir(), "settings"), path, true, func(context.Context, Config) Result { return Result{State: "connected", Message: "ok"} }, nil)
	if _, e := c.Save(c.Current); e == nil {
		t.Fatal("overrode managed configuration")
	}
	if _, e := c.Test(context.Background(), Config{Endpoint: "unix:///tmp/other.sock", Project: "default"}); e == nil {
		t.Fatal("managed connection tested arbitrary target")
	}
	one, e := c.Identity()
	if e != nil || one.PeerID == "" {
		t.Fatal(e)
	}
	if e := Validate(Config{Endpoint: "discobox://" + one.PeerID, Project: "default", Relays: []string{"https://relay.example"}}); e != nil {
		t.Fatalf("offline peer validation initialized a transport: %v", e)
	}
	two, e := c.Identity()
	if e != nil || one.PeerID != two.PeerID {
		t.Fatal("identity changed")
	}
	if e = os.WriteFile(path, []byte("corrupt existing key"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Identity(); e == nil {
		t.Fatal("silently replaced corrupt key")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "corrupt existing key" {
		t.Fatal("corrupt key overwritten")
	}
}
func TestCorruptSettingsCanBeReplacedWithoutDroppingBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte("{broken"), 0600)
	if _, e := Load(path); e == nil {
		t.Fatal("accepted corrupt profile")
	}
	c := New(Config{Endpoint: "unix://", Project: "default"}, path, "", false, nil, errors.New("invalid profile"))
	if c.Status().State != "setup_required" {
		t.Fatal("missing actionable setup state")
	}
	status, e := c.Save(c.Current)
	if e != nil || !status.RestartRequired {
		t.Fatalf("restart omitted after repairing initial failure: %+v %v", status, e)
	}
}
func TestProbeOnlyReadsCatalogAndClassifiesHTTPDenial(t *testing.T) {
	network := testhttp.NewNetwork(t)
	code := 200
	var gets atomic.Int32
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/projects/default/sandboxes" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		gets.Add(1)
		w.WriteHeader(code)
		w.Write([]byte(`{"sandboxes":[]}`))
	}))
	defer server.Close()
	cfg := Config{Endpoint: server.URL, Project: "default"}
	for _, tc := range []struct {
		code  int
		state string
	}{{200, "connected"}, {401, "unauthorized"}, {403, "forbidden"}, {500, "unreachable"}} {
		code = tc.code
		if got := ProbeConfigured(context.Background(), cfg, ""); got.State != tc.state {
			t.Errorf("%d: %+v", tc.code, got)
		}
	}
	if gets.Load() != 4 {
		t.Fatal("probe made extra requests")
	}
}

func TestSaveAppliesInPlaceWhenTheAdapterCan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.json")
	c := New(Config{Endpoint: "unix://", Project: "default"}, path, filepath.Join(t.TempDir(), "key"), false, nil, errors.New("startup failed"))
	var applied []Config
	healthy := false
	c.Apply = func(_ context.Context, cfg Config) (Result, error) {
		applied = append(applied, cfg)
		if !healthy {
			return Result{State: "not_admitted", Message: "Peer reachable but this adapter is not enrolled.", Steps: nil}, errors.New("not_admitted")
		}
		return Result{State: "connected", Message: "ok"}, nil
	}
	candidate := Config{Endpoint: "unix:///tmp/other.sock", Project: "research"}
	status, err := c.SaveContext(context.Background(), candidate)
	if err != nil || status.State != "not_admitted" || !status.RestartRequired || !strings.Contains(status.Message, "not applied") || !status.CanApply {
		t.Fatalf("failed apply: %+v %v", status, err)
	}
	healthy = true
	status, err = c.SaveContext(context.Background(), candidate)
	if err != nil || status.State != "connected" || status.RestartRequired || !reflect.DeepEqual(c.Current, candidate) {
		t.Fatalf("apply: %+v %v", status, err)
	}
	if len(applied) != 2 {
		t.Fatalf("apply calls: %d", len(applied))
	}
	// Saving the active settings again does not rebuild the connection.
	if _, err := c.SaveContext(context.Background(), candidate); err != nil || len(applied) != 2 {
		t.Fatal("re-applied unchanged settings")
	}
	c.Observe(true)
	if c.Status().LastSuccess == nil {
		t.Fatal("passive health not recorded")
	}
}
