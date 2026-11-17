package discobox

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ErrEventsUnsupported means the server has no project event stream.
var ErrEventsUnsupported = errors.New("Discobox server has no project event stream")

// WatchSandboxEvents follows the project's sandbox event stream and calls
// changed for every sandbox create, update, delete or reset. It resumes from
// the last event ID after a disconnect and returns when ctx ends, or with
// ErrEventsUnsupported on a server without the stream. Event payloads are not
// interpreted: the caller re-reads the catalog, which stays the one parser.
func (c *Client) WatchSandboxEvents(ctx context.Context, changed func()) error {
	lastID := ""
	backoff := time.Second
	for {
		started := time.Now()
		err := c.streamEvents(ctx, &lastID, changed)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ErrEventsUnsupported) {
			return err
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		// A dropped stream may have missed changes.
		changed()
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (c *Client) streamEvents(ctx context.Context, lastID *string, changed func()) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/projects/"+c.Session.Project+"/events", nil)
	if err != nil {
		return err
	}
	request.Header = c.headers()
	request.Header.Set("Accept", "text/event-stream")
	if *lastID != "" {
		request.Header.Set("Last-Event-ID", *lastID)
	}
	// The stream outlives the JSON request timeout; ctx bounds it instead.
	client := &http.Client{Transport: c.http.Transport, CheckRedirect: c.http.CheckRedirect}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed:
		return ErrEventsUnsupported
	case response.StatusCode != http.StatusOK:
		return &HTTPError{Status: response.StatusCode, Path: "/api/projects/" + c.Session.Project + "/events"}
	case !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream"):
		return ErrEventsUnsupported
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	event, id, hasData := "", "", false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if id != "" {
				*lastID = id
			}
			// An event with only an id moves the cursor and dispatches nothing.
			if event == "reset" || strings.HasPrefix(event, "sandbox.") || (event == "" && hasData) {
				changed()
			}
			event, id, hasData = "", "", false
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "id":
			if len(value) <= 256 {
				id = value
			}
		case "data":
			hasData = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("Discobox event stream ended")
}
