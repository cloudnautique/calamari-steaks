package main

import (
	"context"
	"errors"
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
	"github.com/cloudnautique/discobox-mcp-ui/internal/connection"
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
	endpoint := flag.String("discobox", os.Getenv("DISCOBOX_MCP_ENDPOINT"), "Discobox HTTP(S) origin, Unix socket, Windows npipe URL or discobox://<peer-id>")
	local := flag.Bool("local", false, "Use this sandbox's pool-forwarded Discobox API; requires approved API access")
	browse := flag.Bool("browse", true, "Start with the manager picker; do not discover or create a manager at startup")
	identity := flag.String("iroh-identity", "", "Persistent adapter enrollment key (default: user config discobox-mcp/iroh/id_ed25519)")
	peerID := flag.Bool("peer-id", false, "Initialize/load the adapter identity, print its public peer ID and exit")
	var relays origins
	flag.Var(&relays, "iroh-relay", "Custom Iroh relay URL; repeat as needed, and match the server configuration")
	project := flag.String("project", "default", "Discobox project ID")
	box := flag.String("box", "", "Existing manager box ID; omitted reuses saved state, discovers a tagged manager, or creates one")
	exec := flag.String("exec", "primary", "Existing exec ID; primary is resolved and pinned at startup")
	managerState := flag.String("manager-state", "", "Manager state file when --box is omitted (default: per-endpoint/project file in user config directory)")
	harness := flag.String("harness", "", "Harness for a new manager (default: project's configured harness)")
	pool := flag.String("pool", "", "Pool ID for a new manager (default: project's configured pool)")
	managerTimeout := flag.Duration("manager-timeout", 3*time.Minute, "Maximum time to create/reconnect a saved manager and find its primary session")
	listen := flag.String("listen", "127.0.0.1:0", "Loopback address for terminal UI and optional HTTP MCP")
	transport := flag.String("transport", "stdio", "MCP transport: stdio or http")
	accessTokenFile := flag.String("access-token-file", "", "Private file containing a stable 64-character hex UI/MCP access token; omitted generates one per process")
	requestLog := flag.Bool("request-log", false, "Log HTTP paths and MCP method timing to stderr, without credentials or request contents")
	var allowed origins
	flag.Var(&allowed, "ui-origin", "Exact MCP iframe origin allowed to connect to the terminal; repeat as needed")
	settingsPath := flag.String("connection-config", "", "Private connection profile path (default: user config discobox-mcp/connection.json)")
	probe := flag.Bool("connection-probe", false, "Internal: bounded connection test using JSON settings on stdin")
	flag.Parse()
	browseExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "browse" {
			browseExplicit = true
		}
	})
	if !browseExplicit && (*box != "" || *managerState != "" || *harness != "" || *pool != "") {
		*browse = false
	}
	token := os.Getenv("DISCOBOX_MCP_TOKEN")
	if *probe {
		return runConnectionProbe(*identity)
	}
	var startupError error
	if *settingsPath == "" {
		*settingsPath, startupError = connection.DefaultPath()
	}
	projectGiven := os.Getenv("DISCOBOX_MCP_PROJECT") != ""
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "project" {
			projectGiven = true
		}
	})
	if os.Getenv("DISCOBOX_MCP_PROJECT") != "" {
		flagProject := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "project" {
				flagProject = true
			}
		})
		if !flagProject {
			*project = os.Getenv("DISCOBOX_MCP_PROJECT")
		}
	}
	managed := *local || *endpoint != "" || projectGiven
	cfg := connection.Config{Endpoint: connection.LocalAddress(), Project: "default"}
	if !*local && !*peerID {
		saved, e := connection.Load(*settingsPath)
		if e == nil {
			cfg = saved
		} else if !errors.Is(e, os.ErrNotExist) && *endpoint == "" {
			startupError = e
		}
	}
	if *endpoint != "" {
		cfg.Endpoint = *endpoint
	}
	if projectGiven {
		cfg.Project = *project
	}
	if len(relays) > 0 {
		cfg.Relays = append([]string(nil), relays...)
	}
	var parent string
	if *local {
		if *endpoint != "" || *peerID || *identity != "" || len(relays) != 0 {
			return fmt.Errorf("--local cannot be combined with an endpoint, --peer-id or Iroh options")
		}
		localConfig, e := discobox.LocalConnection()
		if e != nil {
			startupError = e
		} else {
			cfg.Endpoint, parent, token = localConfig.Endpoint, localConfig.Parent, ""
			if !projectGiven {
				cfg.Project = localConfig.Project
			}
		}
	}
	if *identity == "" {
		var e error
		*identity, e = discobox.DefaultPeerIdentityPath()
		if e != nil {
			startupError = e
		}
	}
	if *peerID {
		_, id, e := discobox.PeerIdentity(*identity)
		if e != nil {
			return e
		}
		fmt.Println(id.String())
		return nil
	}
	if *box == "" && *exec != "primary" {
		return fmt.Errorf("--exec requires --box")
	}
	if *browse && (*box != "" || *exec != "primary") {
		return fmt.Errorf("--browse cannot be combined with --box or --exec; use --browse=false for an explicit startup session")
	}
	if *box != "" && (*managerState != "" || *harness != "" || *pool != "") {
		return fmt.Errorf("manager creation options apply only without --box")
	}
	if *managerTimeout <= 0 {
		return fmt.Errorf("--manager-timeout must be positive")
	}
	if *transport != "stdio" && *transport != "http" {
		return fmt.Errorf("--transport must be stdio or http")
	}
	host, _, e := net.SplitHostPort(*listen)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("--listen must use a loopback IP; remote authentication is not implemented")
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	*endpoint, *project = connection.Resolve(cfg.Endpoint), cfg.Project
	if e := connection.Validate(cfg); e != nil {
		startupError = e
	}
	if startupError == nil && strings.HasPrefix(*endpoint, "discobox://") {
		if _, e := discobox.ConfigurePeer(*endpoint, *identity, cfg.Relays); e != nil {
			startupError = e
		}
	}
	var client *discobox.Client
	if startupError == nil {
		if *browse {
			client, err = discobox.NewCatalog(*endpoint, token, *project)
		} else if *box == "" {
			if *managerState == "" {
				*managerState, err = discobox.DefaultManagerState(*endpoint, *project)
			}
			if err == nil {
				startup, done := context.WithTimeout(ctx, *managerTimeout)
				client, err = discobox.EnsureManager(startup, *endpoint, token, *project, discobox.ManagerOptions{StatePath: *managerState, Harness: *harness, Pool: *pool, Parent: parent, Progress: func(message string) { log.Print(message) }})
				done()
			}
		} else {
			client, err = discobox.New(*endpoint, token, discobox.Session{Project: *project, Box: *box, Exec: *exec})
			if err == nil && parent != "" {
				err = client.CheckManagerParent(ctx, parent)
			}
			if err == nil {
				err = client.ResolvePrimary(ctx)
			}
		}
		if err != nil {
			startupError = err
			client = nil
		}
	}
	if startupError != nil {
		log.Printf("Connection setup required; MCP remains available (initialization error type %T)", startupError)
	}
	onboarding := connection.New(cfg, *settingsPath, *identity, managed, isolatedProbe(*identity, cfg, token), startupError)
	origin := "http://" + listener.Addr().String()
	var accessToken string
	if *accessTokenFile != "" {
		data, err := os.ReadFile(*accessTokenFile)
		if err != nil {
			return err
		}
		accessToken = strings.TrimSpace(string(data))
		if accessToken == "" {
			return fmt.Errorf("access token file is empty")
		}
	}
	s, err := bridge.New(client, origin, allowed, accessToken, discobox.ManagerOptions{StatePath: *managerState, Parent: parent, Harness: *harness, Pool: *pool})
	if err != nil {
		return err
	}
	defer s.CloseServiceForwards()
	defer s.CloseFileUploads()
	s.Onboarding = onboarding
	log.Printf("Connection setup (private link): %s/setup#token=%s", origin, s.Token())
	mcpServer := s.MCP()
	if *requestLog {
		mcpServer.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				started := time.Now()
				var tool string
				var resource string
				switch params := req.GetParams().(type) {
				case *mcp.CallToolParamsRaw:
					tool = params.Name
				case *mcp.CallToolParams:
					tool = params.Name
				case *mcp.ReadResourceParams:
					resource = params.URI
					if index := strings.IndexAny(resource, "?#"); index >= 0 {
						resource = resource[:index]
					}
				}
				log.Printf("MCP start method=%q tool=%q resource=%q", method, tool, resource)
				result, err := next(ctx, method, req)
				failed := err != nil
				if called, ok := result.(*mcp.CallToolResult); ok && called.IsError {
					failed = true
				}
				log.Printf("MCP done method=%q tool=%q resource=%q duration=%s failed=%t", method, tool, resource, time.Since(started).Round(time.Millisecond), failed)
				return result, err
			}
		})
	}
	var mcpHTTP http.Handler
	if *transport == "http" {
		mcpHTTP = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	}
	var handler http.Handler = s.Handler(mcpHTTP)
	if *requestLog {
		inner := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorded := &responseLog{ResponseWriter: w}
			log.Printf("HTTP start method=%q path=%q", r.Method, r.URL.EscapedPath())
			defer func() {
				status := recorded.status
				if status == 0 {
					status = http.StatusOK
				}
				log.Printf("HTTP done method=%q path=%q status=%d duration=%s", r.Method, r.URL.EscapedPath(), status, time.Since(started).Round(time.Millisecond))
			}()
			inner.ServeHTTP(recorded, r)
		})
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
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
	if client != nil && client.Session.Box != "" {
		log.Printf("Manager: %s / %s / %s", client.Session.Project, client.Session.Box, client.Session.Exec)
	} else if client != nil {
		log.Printf("Box picker ready for project %s; no box created at startup", client.Session.Project)
	} else {
		log.Print("Connection setup ready; backend operations are disabled until restart with valid settings")
	}
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
