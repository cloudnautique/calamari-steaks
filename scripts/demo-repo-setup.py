#!/usr/bin/env python3
"""Exercise the repository workflow against a disposable loopback Discobox fixture."""
import argparse
import json
import os
import pathlib
import re
import secrets
import subprocess
import tempfile
import threading
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

PROJECT = "fixture"
BOX = "sbx_fixture_manager"
COMMIT = "0123456789abcdef0123456789abcdef01234567"
CANARY = "fixture-secret-must-never-appear-in-a-receipt"
PATH = "/workspace/source/.discobox/setup-status.json"
ROOT = f"/projects/{PROJECT}"
LAUNCH = {
    "requestId": "fixture-factory-intent",
    "name": "terraform-factory",
    "harness": "claude-code",
    "source": {"repository": "discobox-ai/factory-demo", "branch": "main", "commit": COMMIT},
    "setup": {
        "entrypoint": "/setup", "requestApi": True, "triageOnly": True,
        "actor": "cloudnautique",
        "settingsRepository": "cloudnautique/terraform-provider-obot-factory",
        "issueRepository": "obot-platform/terraform-provider-obot",
        "pullRequestRepository": "obot-platform/terraform-provider-obot",
        "pullRequestBranch": "main",
        "workerPushRepository": "cloudnautique/terraform-provider-obot",
    },
    "task": "Disposable demonstration only. Confirm the settings and stop before real writes.",
}


def now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


class Fixture(ThreadingHTTPServer):
    phase = "boot"
    config = None
    creates = 0
    inputs = 0

    def box(self):
        running = self.phase not in ("boot", "private", "stopped")
        return {
            "id": BOX, "projectId": PROJECT, "config": self.config,
            "discoboxApi": self.phase in ("allowed", "watching", "worker", "stopped"),
            "runtime": {
                "displayState": "running" if running else ("stopped" if self.phase == "stopped" else "awaiting_source"),
                "agentStatusObservedAt": now() if running else "2026-11-19T00:00:00Z",
                "sourceDeliveries": {"primary": {"status": {
                    "boot": "unknown", "private": "needs_credential"
                }.get(self.phase, "delivered")}},
                "agentStatus": {"sourceStates": [{
                    "slug": "primary", "state": "materialized", "commit": COMMIT, "updatedAt": now(),
                }] if running else []},
            },
        }

    def report(self):
        ready = self.phase in ("watching", "worker")
        return {
            "boxId": BOX, "observedAt": now(), "simulated": True,
            "setup": "complete" if ready else "pending",
            "watcher": "verified" if ready else "pending",
            "worker": "running" if self.phase == "worker" else "none",
            "ci": "pending" if self.phase == "worker" else "none",
            "ignoredSecret": CANARY,
        }


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def respond(self, value, code=200):
        self.send_response(code)
        if isinstance(value, bytes):
            self.send_header("Content-Type", "application/octet-stream")
        else:
            self.send_header("Content-Type", "application/json")
            value = json.dumps(value).encode()
        self.end_headers()
        self.wfile.write(value)

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        exec_path = f"/api{ROOT}/sandboxes/{BOX}/execs/exec_fixture"
        if self.path == exec_path + "/input":
            message = body["input"][0]["text"]
            if "terraform-provider-obot-factory" not in message or body["input"][-1].get("key") != "Enter":
                self.send_error(422)
                return
            self.server.inputs += 1
            self.respond({"resumeAfter": "fixture-input"})
            return
        if self.path == exec_path + "/wait":
            if body["until"].get("after") != "fixture-input":
                self.send_error(422)
                return
            self.respond({"reason": "program", "program": {"state": "blocked"}, "resumeAfter": "fixture-blocked"})
            return
        if self.path != ROOT + "/sandboxes":
            self.send_error(405)
            return
        config = body.get("config", {})
        source = config.get("source", {})
        prompt = " ".join(config.get("prompt", []))
        if (body.get("discoboxApi") is not False or body.get("bindings") or
                source.get("url") != "https://github.com/discobox-ai/factory-demo.git" or
                source.get("checkout") != {"commit": COMMIT, "refName": "main", "refType": "branch"} or
                source.get("destination", {}).get("workingDirectory") != "/workspace/source" or
                not all(str(LAUNCH["setup"][key]) in prompt for key in
                        ("entrypoint", "actor", "settingsRepository", "issueRepository", "workerPushRepository"))):
            self.send_error(422)
            return
        self.server.creates += 1
        self.server.config = config
        # This records the settings request only in this temporary process.
        self.respond(self.server.box(), 201)

    def do_GET(self):
        path = urlsplit(self.path).path.removeprefix("/api")
        fixture = self.server
        box_path = f"{ROOT}/sandboxes/{BOX}"
        if path == ROOT:
            self.respond({"defaultHarnessConfigId": "fixture-harness"})
        elif path.startswith("/fixture/"):
            phase = path.rsplit("/", 1)[-1]
            if phase not in ("boot", "private", "limited", "unknown", "pending", "denied", "allowed", "watching", "worker", "stopped"):
                self.send_error(400)
                return
            fixture.phase = phase
            self.respond({"phase": phase, "simulated": True})
        elif path == ROOT + "/sandboxes":
            self.respond({"sandboxes": [fixture.box()] if fixture.config else []})
        elif path == box_path and fixture.config:
            self.respond(fixture.box())
        elif path == box_path + "/execs":
            self.respond({"execs": [{"id": "exec_fixture", "primary": True}] if fixture.box()["runtime"]["displayState"] == "running" else []})
        elif path == box_path + "/execs/exec_fixture/screen":
            self.respond({"text": "SIMULATION. Confirm GitHub actor and destinations for this manager."})
        elif path == ROOT + "/api-access-requests":
            if fixture.phase in ("limited", "unknown"):
                self.respond({"error": CANARY}, 403 if fixture.phase == "limited" else 503)
                return
            history = ([self.request_row("api_denied", "denied")] if fixture.phase in
                       ("denied", "allowed", "watching", "worker", "stopped") else [])
            if fixture.phase in ("allowed", "watching", "worker", "stopped"):
                history.insert(0, self.request_row("api_approved", "approved"))
            if fixture.phase == "pending":
                history = [self.request_row("api_pending", "pending")]
            self.respond({"apiAccessRequests": history})
        elif path == ROOT + "/secret-requests":
            row = self.request_row("sreq_fixture", "approved")
            row.update({"sandboxId": BOX, "name": "github",
                        "use": {"outcome": "approved"},
                        "delegate": {"outcome": "approved" if fixture.phase in ("watching", "worker", "stopped") else "denied"}})
            foreign = {**row, "id": "sreq_unrelated", "sandboxId": "sbx_unrelated"}
            self.respond({"secretRequests": [foreign, row]})
        elif path == ROOT + "/approval-requests":
            self.respond({"approvalRequests": []})
        elif path == box_path + "/files/stat":
            encoded = json.dumps(fixture.report()).encode()
            self.respond({"path": PATH, "type": "file", "size": len(encoded), "version": fixture.phase})
        elif path == box_path + "/files/content":
            self.respond(json.dumps(fixture.report()).encode())
        else:
            self.send_error(404)

    @staticmethod
    def request_row(request_id, status):
        return {"id": request_id, "projectId": PROJECT, "requesterSandboxId": BOX,
                "status": status, "createdAt": now(), "justification": CANARY}


class MCP:
    def __init__(self, process):
        self.process = process
        self.message_id = 0
        self.send("initialize", {"protocolVersion": "2025-11-25",
                                 "capabilities": {}, "clientInfo": {"name": "fixture", "version": "1"}})
        self.send("notifications/initialized")

    def send(self, method, params=None):
        message = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            self.message_id += 1
            message.update(id=self.message_id, params=params)
        self.process.stdin.write(json.dumps(message) + "\n")
        self.process.stdin.flush()
        if params is not None:
            result = json.loads(self.process.stdout.readline())
            assert result.get("id") == self.message_id and "error" not in result, result
            return result["result"]

    def call(self, name, arguments, *, error=False):
        result = self.send("tools/call", {"name": name, "arguments": arguments})
        assert bool(result.get("isError")) == error, result
        assert CANARY not in json.dumps(result), "raw fixture data crossed the MCP boundary"
        return result


def walk(client, fixture):
    def observe(phase, *expected):
        fixture.phase = phase
        result = client.call("setup_status", {"boxId": BOX})
        assert result["structuredContent"]["boxId"] == BOX
        visible = result["content"][0]["text"]
        assert all(text in visible for text in expected), visible
        assert "sreq_unrelated" not in visible
        print(f"\n[SIMULATED {phase}]\n{visible}")
        return result

    result = client.call("launch_box", LAUNCH)
    receipt = result["content"][0]["text"]
    assert len(json.dumps(result)) < 6000
    assert result["structuredContent"]["boxId"] == BOX
    assert "Start with triage only" in receipt
    for destination in ("actor", "settingsRepository", "issueRepository", "workerPushRepository"):
        assert LAUNCH["setup"][destination] in receipt
    print(f"[SIMULATED launch]\n{receipt}")
    assert client.call("launch_box", LAUNCH)["structuredContent"]["boxId"] == BOX
    client.call("launch_box", {**LAUNCH, "source": {**LAUNCH["source"], "branch": "different"}}, error=True)
    assert fixture.creates == 1
    observe("private", "needs_credential", "discobox attach " + BOX)
    observe("limited", "limited by caller role", "Secrets (F4)")
    observe("unknown", "unknown (request failed)", "Secrets (F4)")
    observe("pending", "api_pending · pending", "Setup pending")
    observe("denied", "api_denied · denied", "use approved · delegation denied")
    observe("allowed", "api_denied · denied", "api_approved · approved", "Discobox API enabled", "watcher pending")
    question = client.call("read_box", {"boxId": BOX, "scrollback": 20})
    assert "Confirm GitHub actor and destinations" in question["content"][0]["text"]
    answer = client.call("send_box", {"boxId": BOX, "submit": True,
        "text": "Use cloudnautique/terraform-provider-obot-factory for settings and confirm it on this manager. Triage only in this fixture."})
    wait = client.call("wait_box", {"boxId": BOX, "after": answer["structuredContent"]["resumeAfter"], "until": "needs_input"})
    assert wait["structuredContent"]["resumeAfter"] == "fixture-blocked"
    assert fixture.inputs == 1
    observe("watching", "Setup complete · watcher verified", "simulated fixture report", "child worker none · CI none")
    observe("worker", "child worker running · CI pending", "reported revision " + COMMIT)
    opened = client.call("open_box", {"boxId": BOX})
    assert opened["structuredContent"]["session"]["box"] == BOX
    screen = client.call("read_box", {"boxId": BOX, "scrollback": 20})
    assert "SIMULATION" in screen["content"][0]["text"]
    observe("stopped", "stopped", "Last agent observation", "watcher, child worker and CI are unverified")
    assert fixture.creates == 1
    print("\nPASS: one create, same box across replay, console return, and terminal read/send/wait.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("--browser", action="store_true", help="serve the same workflow for manual widget interaction")
    args = parser.parse_args()
    with Fixture(("127.0.0.1", 0), Handler) as fixture, tempfile.TemporaryDirectory(prefix="discobox-mcp-demo-") as directory:
        threading.Thread(target=fixture.serve_forever, daemon=True).start()
        url = f"http://127.0.0.1:{fixture.server_port}"
        env = {**os.environ, "XDG_CONFIG_HOME": directory, "APPDATA": directory, "NO_PROXY": "127.0.0.1"}
        env.pop("DISCOBOX_MCP_TOKEN", None)
        command = [str(args.binary.resolve()), "--discobox", url, "--project", PROJECT,
                   "--browse=true", "--manager-state", f"{directory}/manager.json",
                   "--connection-config", f"{directory}/connection.json"]
        token = secrets.token_hex(32)
        if args.browser:
            token_path = pathlib.Path(directory, "access")
            token_path.write_text(token)
            token_path.chmod(0o600)
            command += ["--browser-ui", "--access-token-file", str(token_path)]
        with subprocess.Popen(command, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, text=True) as process:
            try:
                if args.browser:
                    for line in process.stderr:
                        match = re.search(r"Local browser workspace .*?(http://127\.0\.0\.1:\d+)", line)
                        if match:
                            print(f"Widget: {match[1]}/#token={token}", flush=True)
                            break
                    print("Use the launch fields from the demo script. This link only controls the disposable fixture.", flush=True)
                    print(f"Change phase with {url}/fixture/<phase>; use Refresh setup in the widget.", flush=True)
                    try:
                        process.wait()
                    except KeyboardInterrupt:
                        pass
                else:
                    walk(MCP(process), fixture)
            finally:
                process.terminate()
                fixture.shutdown()


if __name__ == "__main__":
    main()
