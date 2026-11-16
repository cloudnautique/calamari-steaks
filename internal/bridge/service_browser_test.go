package bridge

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestForwardLifetimeAndNativeBrowserScope(t *testing.T) {
	s, _, _ := serviceFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	info, err := s.openForward(ctx, serviceInput{BoxID: "box", Port: 8080})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	// Like leaving the widget or opening another browser, ending the request
	// does not close the listener. It belongs to the adapter's lifetime.
	c, err := net.DialTimeout("tcp", info.Address, time.Second)
	if err != nil {
		t.Fatal("forward was tied to opening request", err)
	}
	c.Close()
	list := s.forwards("box")
	if len(list) != 1 || list[0].ExpiresAt.Before(time.Now().Add(14*time.Minute)) {
		t.Fatal("missing lease information")
	}
	if _, err = s.openServiceBrowser("missing"); err == nil {
		t.Fatal("arbitrary browser target accepted")
	}
	s.closeForwardReason(info.ID, "Fixture lease expired")
	if _, err = s.openServiceBrowser(info.ID); err == nil {
		t.Fatal("closed forward launched browser")
	}
	closed := s.closedForwards("box")
	if len(closed) != 1 || !strings.Contains(closed[0].Message, "expired") || closed[0].State != "closed" {
		t.Fatal("missing close diagnostics")
	}
	if len(s.closedForwards("another")) != 0 {
		t.Fatal("history leaked another box")
	}
	for _, platform := range []string{"darwin", "windows", "linux"} {
		_, args, err := browserCommand(platform, "http://127.0.0.1:61335/")
		if err != nil || args[len(args)-1] != "http://127.0.0.1:61335/" {
			t.Fatal("browser URL was shell-expanded", platform)
		}
	}
}
