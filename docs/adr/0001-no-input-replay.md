# 0001: Never replay input or creation

Status: accepted (recorded 2026-11-17; the rule predates this record).

A write whose outcome is uncertain (terminal input, `send_box`, box creation,
approval decisions, lifecycle actions, uploads) may already have run. The
adapter never retries one automatically and never moves queued input to another
session. Creation is journalled under a caller-supplied `requestId`; asking
again with the same key reconciles instead of creating a second box. Errors say
what to check before trying again.

Consequences: reconnects restore observation only; a replaced session is
adopted only on an explicit reopen and only while no person holds input.
