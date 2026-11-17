package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Regression for SECURITY-REVIEW.md H1: DISCOBOX_MCP_TOKEN reaches only an
// endpoint pinned by the launcher, never one named by the saved profile.
func TestUpstreamTokenIsBoundToLauncherEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a Unix socket")
	}
	for _, pinned := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved profile", true: "launcher endpoint"}[pinned], func(t *testing.T) {
			// Keep the socket path short for macOS sun_path limits.
			dir, err := os.MkdirTemp("", "dbx")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "api.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var requests int
			var authorization []string
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests++
				if value := r.Header.Get("Authorization"); value != "" {
					authorization = append(authorization, value)
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"sandboxes":[]}`))
			})}
			go server.Serve(listener)
			defer server.Close()

			endpoint := "unix://" + socket
			profile := filepath.Join(dir, "connection.json")
			data, _ := json.Marshal(map[string]string{"endpoint": endpoint, "project": "p"})
			if err := os.WriteFile(profile, data, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBootstrapProcess$", "--", "--connection-config", profile, "--iroh-identity", filepath.Join(dir, "key.pem"))
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if !strings.HasPrefix(key, "DISCOBOX_") {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "DISCOBOX_TEST_BOOTSTRAP=1", "DISCOBOX_MCP_TOKEN=fixture-upstream-credential")
			if pinned {
				command.Env = append(command.Env, "DISCOBOX_MCP_ENDPOINT="+endpoint, "DISCOBOX_MCP_PROJECT=p")
			}
			command.Stderr = io.Discard
			client := mcp.NewClient(&mcp.Implementation{Name: "token-binding-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_boxes", Arguments: map[string]any{}}); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if requests == 0 {
				t.Fatal("adapter never reached the fixture endpoint")
			}
			if pinned && (len(authorization) == 0 || authorization[0] != "Bearer fixture-upstream-credential") {
				t.Fatalf("launcher-pinned endpoint did not receive the token: %q", authorization)
			}
			if !pinned && len(authorization) != 0 {
				t.Fatalf("saved-profile endpoint received the launcher token")
			}
		})
	}
}

// Regression for SECURITY-REVIEW.md H2: a stable access token supplied by file
// is never echoed to stderr, which a declared service exposes as logs.
func TestAccessTokenFromFileIsNotLogged(t *testing.T) {
	dir := t.TempDir()
	const fixture = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tokenFile := filepath.Join(dir, "access-token")
	if err := os.WriteFile(tokenFile, []byte(fixture+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBootstrapProcess$", "--", "--connection-config", filepath.Join(dir, "connection.json"), "--iroh-identity", filepath.Join(dir, "key.pem"), "--access-token-file", tokenFile)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "DISCOBOX_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "DISCOBOX_TEST_BOOTSTRAP=1", "DISCOBOX_SERVER_ADDRESS=unix://"+filepath.Join(dir, "missing.sock"))
	var stderr strings.Builder
	command.Stderr = &stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "token-log-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	session.Close()
	logged := stderr.String()
	if !strings.Contains(logged, "Local terminal (private link): http://127.0.0.1:") {
		t.Fatalf("private link line missing from stderr:\n%s", logged)
	}
	if strings.Contains(logged, fixture) {
		t.Fatal("access token from file was written to stderr")
	}
}

// Regression for SECURITY-REVIEW.md H9: shared secret files are reported.
func TestSharedSecretWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits do not describe Windows ACLs")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if warning := sharedSecretWarning(path); warning != "" {
		t.Fatalf("private file reported: %s", warning)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if warning := sharedSecretWarning(path); !strings.Contains(warning, "0644") {
		t.Fatalf("shared file not reported: %q", warning)
	}
	if warning := sharedSecretWarning(filepath.Join(dir, "missing")); warning != "" {
		t.Fatal("missing file reported")
	}
}
