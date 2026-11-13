# One box list, one workspace

Draft, not a change to live API behavior. The interactive companion is
`boxes-ux-draft.html`. It uses mock data and never makes API requests.

## Model and scope

A manager is a box with a coordinator role. Show a small Manager badge, and use
exactly the same open, message, terminal, desktop, and archive interactions for
it as for any other box. Harness and role are separate: either role may use Codex,
Claude, or another supported harness. Avoid separate manager navigation.

List every box visible through the configured Discobox connection, including
boxes created outside this integration. The current adapter still filters on
`discobox-mcp-role` and refuses untagged boxes in its connect/archive guard; both
constraints need to change together. Removing only the listing filter is not
sufficient. Keep upstream authorization checks, scoped project/endpoint routing,
and explicit box IDs for every read, write and view.

Visibility does not imply control. A sandbox connection may list a box it cannot
lead. Keep its card visible and explain which actions need adoption approval.
Never imply the list covers boxes the API does not expose. Show archived boxes
only when requested, and distinguish unavailable inventory from an empty list.

## List cards and children

Cards show name, harness, box power state, and separately the agent's reported
activity. Add Manager only when the actual role says so. Open is the main action;
Terminal is a shortcut to the same workspace's terminal tab.

Nest known children in a collapsible section of the parent card. Use the actual
parent ID, regardless of role or creation source. Keep nesting shallow; deeper
relationships can use a parent breadcrumb in the detail view. Missing parents
never hide children: those boxes remain top-level. Handle cycles by promoting
unplaced entries to top-level rather than dropping them. Filtering to Needs you
retains the ancestors needed to place matching children, and marks counts as
visible children rather than total children if the API is partial.

## App sizing

Respond to the actual app container, not the desktop client name. A narrow
Claude pane can behave like a narrow Codex pane. Container widths in this draft:

- Up to 760 px: list OR workspace, with an explicit Back to boxes button.
- Above 760 px: list beside the selected workspace.
- Short panes: scroll the content; keep the destination and composer reachable.

Use host-provided available dimensions/display mode when supported, and observe
the rendered container. A larger host mode can be requested; acceptance must not
be assumed. Avoid a fixed app height driving repeated auto-size feedback. Keep
flex/grid children shrinkable, allow toolbars to wrap, and size terminal/desktop
viewers from their own available pane after layout. Changing tabs or focus must
never measure a hidden pane as a tiny PTY. A real terminal preserves cells rather
than wrapping a screenshot or stretching a canvas to fake a fit.

Layout and input ownership are separate. The shell adapts regardless of focus.
The shared terminal follows its active user's measured grid; observers preserve
that grid and can scroll when necessary. Focus in the composer/terminal pauses
assistant writes. Blur yields after pending input; window blur/disconnect also
yield. Display who can write. Never replay input on a resize or reconnection.

## Workspace

Keep three tabs: Overview, Terminal, Desktop. Overview contains the task, latest
reported output, attention requests, and eventual results/links. Do not invent a
structured conversation from a terminal screen. Start desktop transport only
when explicitly opened. Keep files/results/services in a small expandable
section until real data warrants more UI.

The composer always names its exact destination. The inbox is shared across
roles and surfaces its permission/coverage limits. Reopening the widget restores
backend state; widget action receipts keep the desktop assistant informed.

## Implementation sequence

1. Unify catalog scope and authorization-aware access to existing boxes. Expose
   one canonical box vocabulary in model-facing tools; preserve legacy manager
   calls during migration without teaching users two independent object types.
2. Replace separate manager controls with responsive box list/workspace, badge,
   known-child grouping, and uniform actions. Carry IDs through every action.
3. Verify narrow/wide/short host panes, reopening, focus handoff, child/missing
   parent cases, inaccessible boxes, and uncertain input/creation outcomes.
4. Add real desktop/results/services only behind working backend data/transport.
