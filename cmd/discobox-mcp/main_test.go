package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run the real bootstrap in a child, with no installed server or user profile.
func TestBootstrapProcess(t *testing.T) {
	if os.Getenv("DISCOBOX_TEST_BOOTSTRAP") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"discobox-mcp"}, os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("discobox-mcp", flag.ContinueOnError)
	if e := run(); e != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestMCPBootstrapSurvivesMissingBackendAndBrokenSettings(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing socket", true: "corrupt profile"}[broken], func(t *testing.T) {
			dir := t.TempDir()
			profile := filepath.Join(dir, "connection.json")
			if broken {
				if e := os.WriteFile(profile, []byte("{broken"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBootstrapProcess$", "--", "--connection-config", profile, "--iroh-identity", filepath.Join(dir, "key.pem"))
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if key != "DISCOBOX_MCP_ENDPOINT" && key != "DISCOBOX_MCP_PROJECT" && key != "DISCOBOX_MCP_TOKEN" && key != "DISCOBOX_SERVER_ADDRESS" {
					command.Env = append(command.Env, entry)
				}
			}
			missing := "unix://" + filepath.Join(dir, "missing.sock")
			if runtime.GOOS == "windows" {
				missing = "npipe:////./pipe/discobox-missing-" + filepath.Base(dir)
			}
			command.Env = append(command.Env, "DISCOBOX_TEST_BOOTSTRAP=1", "DISCOBOX_SERVER_ADDRESS="+missing)
			command.Stderr = io.Discard
			client := mcp.NewClient(&mcp.Implementation{Name: "bootstrap-test", Version: "1"}, nil)
			session, e := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer session.Close()
			tools, e := session.ListTools(ctx, nil)
			if e != nil || len(tools.Tools) != 43 {
				t.Fatalf("discovery failed: %v %v", tools, e)
			}
			r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "open_connection_setup", Arguments: map[string]any{}})
			if e != nil || r.IsError {
				t.Fatalf("setup unavailable: %+v %v", r, e)
			}
			if broken {
				r, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "read_box", Arguments: map[string]any{"boxId": "fixture"}})
				if e != nil || !r.IsError {
					t.Fatalf("ungated invalid client: %+v %v", r, e)
				}
			}
		})
	}
}
