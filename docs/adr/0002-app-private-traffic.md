# 0002: App-private traffic stays out of model context

Status: accepted (recorded 2026-11-17).

Terminal bytes, desktop pixels and input, upload chunks, private service logs
and approval review tokens travel in app-only tools (`_meta.ui.visibility:
["app"]`) or in `_meta`, never in model-visible content. The model receives
summaries, explicit shares the person chose (`ui/message`) and short action
receipts through `updateModelContext`, which a widget sends only after the
person interacts with it.

Hosts enforce visibility; they may log app traffic. Do not describe this as
private beyond "not placed in model context" until each host is checked.
