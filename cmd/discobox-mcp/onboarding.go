package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/bridge"
	"github.com/cloudnautique/discobox-mcp-ui/internal/connection"
	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
)

// runConnectionProbe tests draft settings in a child process, so a draft
// Iroh endpoint and its sockets go away with the process.
func runConnectionProbe(identity string) error {
	var cfg connection.Config
	d := json.NewDecoder(io.LimitReader(os.Stdin, 16385))
	d.DisallowUnknownFields()
	result := connection.Result{State: "invalid_settings", Message: "Invalid connection settings."}
	if e := d.Decode(&cfg); e == nil && connection.Validate(cfg) == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 11*time.Second)
		defer cancel()
		conn, e := discobox.Connect(connection.Resolve(cfg.Endpoint), os.Getenv("DISCOBOX_MCP_TOKEN"), cfg.Project, identity, cfg.Relays)
		if e != nil {
			result = connection.Result{State: "identity_error", Message: "The persistent identity or Iroh transport could not be initialized. Keep the existing key; do not replace it automatically."}
		} else {
			result = connection.ProbeConnection(ctx, conn)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

// applyConnection switches the running adapter to saved settings: it builds
// a new connection generation, checks it, and only then hands it to the
// bridge. Open views stay on the connection they were opened with.
func applyConnection(s *bridge.Server, identity string, launched connection.Config, token string) connection.Apply {
	return func(ctx context.Context, cfg connection.Config) (connection.Result, error) {
		resolved := connection.Resolve(cfg.Endpoint)
		upstream := ""
		if token != "" && cfg.Endpoint == launched.Endpoint && !strings.HasPrefix(resolved, "discobox://") {
			upstream = token
		}
		conn, e := discobox.Connect(resolved, upstream, cfg.Project, identity, cfg.Relays)
		if e != nil {
			return connection.Result{State: "identity_error", Message: "The identity or transport could not be initialized."}, e
		}
		result := connection.ProbeConnection(ctx, conn)
		if result.State != "connected" {
			return result, errors.New(result.State)
		}
		s.UseBackend(conn.Client)
		return result, nil
	}
}

func isolatedProbe(identity string, current connection.Config, token string) connection.Probe {
	return func(ctx context.Context, cfg connection.Config) connection.Result {
		fail := connection.Result{State: "unreachable", Message: "Connection test could not complete. Check the server, listener, network, and identity. A timeout does not prove enrollment denial."}
		binary, e := os.Executable()
		if e != nil {
			return fail
		}
		command := exec.CommandContext(ctx, binary, "--connection-probe", "--iroh-identity", identity)
		// Never move the launcher's upstream credential to a different draft target.
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if name != "DISCOBOX_MCP_TOKEN" && name != "DISCOBOX_MCP_ENDPOINT" && name != "DISCOBOX_MCP_PROJECT" {
				command.Env = append(command.Env, entry)
			}
		}
		if token != "" && cfg.Endpoint == current.Endpoint && !strings.HasPrefix(connection.Resolve(cfg.Endpoint), "discobox://") {
			command.Env = append(command.Env, "DISCOBOX_MCP_TOKEN="+token)
		}
		data, _ := json.Marshal(cfg)
		command.Stdin = strings.NewReader(string(data))
		command.Stderr = io.Discard
		output, e := command.Output()
		if e != nil {
			return fail
		}
		var result connection.Result
		if len(output) > 4096 || json.Unmarshal(output, &result) != nil || result.State == "" {
			return fail
		}
		return result
	}
}
