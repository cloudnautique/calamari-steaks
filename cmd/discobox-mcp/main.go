package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/bridge"
	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type origins []string

func (o *origins) String() string         { return strings.Join(*o, ",") }
func (o *origins) Set(value string) error { *o = append(*o, value); return nil }

func main() {
	log.SetOutput(os.Stderr)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	endpoint := flag.String("discobox", os.Getenv("DISCOBOX_MCP_ENDPOINT"), "Discobox origin or unix:///absolute/server.sock (required)")
	project := flag.String("project", "default", "Discobox project ID")
	box := flag.String("box", "", "Existing manager box ID; omitted reuses saved state, discovers a tagged manager, or creates one")
	exec := flag.String("exec", "primary", "Existing exec ID; primary is resolved and pinned at startup")
	managerState := flag.String("manager-state", "", "Manager state file when --box is omitted (default: per-endpoint/project file in user config directory)")
	harness := flag.String("harness", "", "Harness for a new manager (default: project's configured harness)")
	pool := flag.String("pool", "", "Pool ID for a new manager (default: project's configured pool)")
	managerTimeout := flag.Duration("manager-timeout", 3*time.Minute, "Maximum time to create/reconnect a saved manager and find its primary session")
	listen := flag.String("listen", "127.0.0.1:0", "Loopback address for terminal UI and optional HTTP MCP")
	transport := flag.String("transport", "stdio", "MCP transport: stdio or http")
	var allowed origins
	flag.Var(&allowed, "ui-origin", "Exact MCP iframe origin allowed to connect to the terminal; repeat as needed")
	flag.Parse()
	if *endpoint == "" {
		return fmt.Errorf("--discobox or DISCOBOX_MCP_ENDPOINT is required")
	}
	if *box == "" && *exec != "primary" {
		return fmt.Errorf("--exec requires --box; a created manager uses its primary session")
	}
	if *box != "" && (*managerState != "" || *harness != "" || *pool != "") {
		return fmt.Errorf("--manager-state, --harness and --pool apply only when --box is omitted")
	}
	if *managerTimeout <= 0 {
		return fmt.Errorf("--manager-timeout must be positive")
	}
	if *transport != "stdio" && *transport != "http" {
		return fmt.Errorf("--transport must be stdio or http")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("--listen must use a loopback IP; remote authentication is not implemented")
	}
	// Reserve the local listener before creating any backend resources.
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var client *discobox.Client
	if *box == "" {
		if *managerState == "" {
			*managerState, err = discobox.DefaultManagerState(*endpoint, *project)
			if err != nil {
				return err
			}
		}
		log.Printf("Manager state: %s", *managerState)
		startup, done := context.WithTimeout(ctx, *managerTimeout)
		client, err = discobox.EnsureManager(startup, *endpoint, os.Getenv("DISCOBOX_MCP_TOKEN"), *project, discobox.ManagerOptions{
			StatePath: *managerState, Harness: *harness, Pool: *pool, Progress: func(message string) { log.Print(message) },
		})
		done()
	} else {
		client, err = discobox.New(*endpoint, os.Getenv("DISCOBOX_MCP_TOKEN"), discobox.Session{Project: *project, Box: *box, Exec: *exec})
		if err == nil {
			err = client.ResolvePrimary(ctx)
		}
	}
	if err != nil {
		return err
	}
	origin := "http://" + listener.Addr().String()
	s, err := bridge.New(client, origin, allowed)
	if err != nil {
		return err
	}
	mcpServer := s.MCP()
	var mcpHTTP http.Handler
	if *transport == "http" {
		mcpHTTP = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	}
	httpServer := &http.Server{Handler: s.Handler(mcpHTTP), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		serveErr <- err
		cancel()
	}()
	defer func() {
		cancel()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		httpServer.Shutdown(shutdown)
	}()
	log.Printf("Manager: %s / %s / %s", client.Session.Project, client.Session.Box, client.Session.Exec)
	log.Printf("Local terminal (private link): %s", s.BrowserURL())
	if err := web.Ready(); err != nil {
		log.Printf("Terminal UI not built: %v. MCP read/send/wait remain available.", err)
	}
	if *transport == "stdio" {
		err := mcpServer.Run(ctx, &mcp.StdioTransport{})
		select {
		case serverErr := <-serveErr:
			if serverErr != http.ErrServerClosed {
				return serverErr
			}
		default:
		}
		return err
	}
	log.Printf("MCP: %s/mcp; Authorization: Bearer %s", origin, s.Token())
	select {
	case <-ctx.Done():
		select {
		case err := <-serveErr:
			if err != http.ErrServerClosed {
				return err
			}
		default:
		}
		return nil
	case err := <-serveErr:
		return err
	}
}
