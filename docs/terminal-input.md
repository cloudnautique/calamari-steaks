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

`show_box_view(boxId, view, filePath?)` provides the server-tool fallback and
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
