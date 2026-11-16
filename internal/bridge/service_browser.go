package bridge

import (
	"context"
	"errors"
	"os/exec"
	"runtime"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func browserCommand(goos, target string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{target}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}, nil
	case "linux":
		return "xdg-open", []string{target}, nil
	}
	return "", nil, errors.New("default browser opening is unavailable on this platform; copy the address")
}
func (s *Server) serviceBrowserTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "service_open_browser", Description: "App-only: open this adapter's existing HTTP(S) forward in the default browser on the adapter machine. Only on an explicit button click; never opens arbitrary URLs or creates a forward.", Meta: mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}}, func(ctx context.Context, _ *mcp.CallToolRequest, in forwardInput) (*mcp.CallToolResult, any, error) {
		data, err := s.openServiceBrowser(in.ForwardID)
		if err != nil {
			return nil, nil, err
		}
		return servicePrivate(data), nil, nil
	})
}

func (s *Server) openServiceBrowser(id string) (map[string]any, error) {
	s.serviceMu.Lock()
	f := s.serviceForwards[id]
	s.serviceMu.Unlock()
	if f == nil || f.info.URL == "" {
		return nil, errors.New("forward is closed or has no HTTP(S) URL; refresh Services")
	}
	if err := s.keepForward(id); err != nil {
		return nil, err
	}
	program, args, err := browserCommand(runtime.GOOS, f.info.URL)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(program, args...)
	if err = cmd.Start(); err != nil {
		return nil, errors.New("could not launch the adapter machine's default browser; copy the forward address")
	}
	go cmd.Wait()
	return map[string]any{"message": "Requested the default browser on the adapter machine. Browser launch is not proof the page loaded."}, nil
}
