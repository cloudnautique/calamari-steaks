package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/connection"
	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
)

func runConnectionProbe(identity string) error {
	var cfg connection.Config
	d := json.NewDecoder(io.LimitReader(os.Stdin, 16385))
	d.DisallowUnknownFields()
	result := connection.Result{State: "invalid_settings", Message: "Invalid connection settings."}
	if e := d.Decode(&cfg); e == nil && connection.Validate(cfg) == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ready := true
		if strings.HasPrefix(connection.Resolve(cfg.Endpoint), "discobox://") {
			if _, e := discobox.ConfigurePeer(connection.Resolve(cfg.Endpoint), identity, cfg.Relays); e != nil {
				ready = false
				result = connection.Result{State: "identity_error", Message: "The persistent identity or Iroh transport could not be initialized. Keep the existing key; do not replace it automatically."}
			}
		}
		if ready {
			result = connection.ProbeConfigured(ctx, cfg, os.Getenv("DISCOBOX_MCP_TOKEN"))
		}
	}
	return json.NewEncoder(os.Stdout).Encode(result)
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
