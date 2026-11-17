package discobox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// BoxPower asks Discobox to start, stop or restart a box, or to restore an
// archived one. Each is one explicit request and is never retried here; an
// uncertain result must be checked by listing boxes before asking again.
func (c *Client) BoxPower(ctx context.Context, id, parent, action string) (map[string]any, error) {
	box, err := c.managerBox(ctx, id, parent)
	if err != nil {
		return nil, err
	}
	archived := managerLifecycleError(box) != nil
	var route string
	var body any
	switch action {
	case "restore":
		if !archived {
			return nil, errors.New("box is not archived")
		}
		route = "/unarchive"
	case "start", "restart":
		route = "/" + action
	case "stop":
		route, body = "/stop", map[string]any{}
	default:
		return nil, errors.New("action must be start, stop, restart or restore")
	}
	if archived && action != "restore" {
		return nil, errors.New("box is archived; restore it first")
	}
	result, err := c.request(ctx, http.MethodPost, "/projects/"+c.Session.Project+"/sandboxes/"+id+route, body)
	if err != nil {
		var status *HTTPError
		if errors.As(err, &status) && status.Status == http.StatusConflict {
			return nil, fmt.Errorf("Discobox refused to %s this box in its current state; list boxes to see it", action)
		}
		return nil, fmt.Errorf("%s was not confirmed; list boxes to check the box before asking again: %w", action, err)
	}
	return result, nil
}
