# Terminal input and view navigation

The terminal is the widget's input. The separate whole-instruction composer has
been removed. Text-only assistant interaction remains available through the
existing send_box/read_box/wait_box tools.

## Clipboard

The adapter intercepts clipboard shortcuts and clipboard events before Ghostty's
keyboard encoder and duplicate paste handlers. Cmd+V, Ctrl+V, Ctrl+Shift+V and
Shift+Insert keep native paste behavior; physical key codes are not required.
Clipboard text goes through Terminal.paste once, preserving bracketed paste when
the remote application enables it. No Enter is appended. Cmd+C/native copy uses
the terminal's selection; plain Ctrl+C still reaches the remote process.

Paste is also available in the toolbar. It reads the clipboard only after that
explicit button press. If the iframe's clipboard API is unavailable or denied,
a transient paste dialog accepts text through a normal browser textarea. Confirm
pastes it into the same pinned box/exec; closing clears the field. This dialog is
a clipboard fallback, not another message composer. It cannot submit approvals,
retarget a changed terminal, or place clipboard text into chat context.

Pastes are validated before sending, with a 64 KiB input-frame limit including
space for bracketed-paste markers. Oversized input is rejected rather than partly
sent. No automatic input retry is introduced. The host may intercept native
shortcuts or restrict clipboard access; the explicit Paste fallback is provided
for that case. Native Claude/Codex keyboard behavior still needs client testing.

The Copy toolbar button copies the terminal selection. Clipboard-write permission
is requested on the UI resource. If the host refuses it, a selected-text dialog
supports native Cmd+C/Ctrl+C without sending anything to chat. Hold Shift while
dragging if the terminal program captures mouse input.

HTTP(S) terminal links open through the MCP host's `openLink` bridge. The Links
button also lists recent links with Open/Copy actions, joining wrapped lines
so login codes and query strings survive copying. These URLs stay local to the
widget. This link handler accepts only HTTP(S) URLs without embedded username/password credentials.

## Model-selected views

A widget advertises an app tool:

```
select_view({boxId, view: "terminal" | "files" | "desktop", filePath?})
```

When the host exposes app tools to its model, this selects a tab in that existing
widget. The requested box must match the widget's current box. A Files path is
optional; no path shows the current browser. The tool refuses while the person
has terminal/desktop control, is editing a field, or has the paste dialog open.
It selects a view without focusing an input or sending any keyboard/mouse input.
The terminal renderer is mounted inert to suppress its startup autofocus.

`open_box(boxId, view, filePath?)` provides the server-tool fallback (formerly
`show_box_view`, still served with `--legacy-manager-tools`) and
links to the same MCP App resource. The host decides whether to reuse a widget
or open another one. Consequently a successful server tool is a view request,
not proof that a particular existing canvas changed. Model context includes
view_selected/view_selection_blocked receipts. Incoming view requests are also
blocked while the person uses the existing widget. Content loading may fail even
when the tab itself was selected; selecting Desktop does not establish a VNC
connection or give the model vision of it.

Both paths obey existing Discobox permissions. In particular the sandbox-role
TCP restriction on live Desktop remains; use an authorized host connection for
that transport. File browsing does not require a primary terminal.

## Verification

The opaque-frame browser check exercised Cmd+V with a missing physical key code,
Unicode/multiline clipboard content, exactly one bracketed paste without Enter,
and a clipboard-denied fallback that clears its text. It also called select_view
through the host bridge to select Files/Desktop/Terminal, rejected selection
during human control and the paste dialog, and verified no keyboard claim from
navigation. Clipboard content stayed out of model context. The iframe made no
direct network requests and raised no browser exceptions. All input and desktop
traffic in this check used isolated fixtures.

Go tests cover server-view metadata, scoped box access, invalid selections, and
Files/Desktop navigation without primary-exec discovery. Node tests cover
clipboard shortcut recognition, bounds, preserved content, and navigation guards.
Native client checks remain pending, especially the reported stray `u` symptom.

## Canvas sizing

Fixed host `containerDimensions.height` is allocated space and is used even when
`displayMode` is absent or says inline. Maximum-only inline containers remain
compact at 520px; fullscreen and picture-in-picture containers follow their
actual iframe viewport, within the host maximum. The box catalog follows the
same rule instead of imposing its own 520px cap in an expanded canvas. Terminal,
Files and Desktop use the remaining flex space below workspace controls.

An opaque-frame browser check verified full Files/footer height at 1120px,
terminal resize from 56 rows at 1120px to 25 rows at 650px, a 1060px expanded
picture-in-picture catalog, and a return to the compact 520px inline catalog.
Native Codex canvas behavior still needs confirmation. Resource v12 retains all
older resource URI aliases so restored views remain readable.

The workspace now uses its requested height directly rather than capping it at
the iframe's initial viewport height. An initially short iframe can request its
compact 520px height. Expand/Compact is available when the host advertises
fullscreen support. Font readiness and viewport changes trigger terminal fitting.
The current resource is v14; all older resource aliases remain readable.
