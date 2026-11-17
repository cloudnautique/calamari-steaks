# 0003: Connection changes create a new generation

Status: accepted (2026-11-17).

Saving a connection profile builds a new client with its own transport
(`discobox.Connect`, one Iroh endpoint per remote generation), diagnoses it,
reads the project catalog, and only then makes it the adapter's backend for new
operations. Open terminals, desktops, forwards and uploads keep the client they
were opened with until they close; pending approval review tokens are dropped.

Trade-off: upstream `IrohEndpoint` has no exported Close, so a replaced remote
generation's endpoint lives until the process exits. Applies are rare, explicit
actions, so this is accepted. Draft tests still run in a child process so
repeated tests do not accumulate endpoints.
