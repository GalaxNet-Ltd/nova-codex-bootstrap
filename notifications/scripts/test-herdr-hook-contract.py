#!/usr/bin/env python3
"""Exercise pinned Herdr hooks in two disposable, detached server namespaces.

Does not use the maintainer's Herdr registry, sessions or Codex hook settings.
Run with HERDR_TEST_BINARY pointing to the existing Herdr 0.9.5 executable.
"""
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time


def request(path, method, params):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(3)
        connection.connect(str(path))
        connection.sendall((json.dumps({"id": "novascale-test", "method": method, "params": params}) + "\n").encode())
        reply = json.loads(connection.makefile("rb").readline(1024 * 1024))
        if reply.get("error"):
            raise RuntimeError(method + " failed: " + str(reply["error"].get("code")))
        return reply["result"]


def main():
    binary = os.environ.get("HERDR_TEST_BINARY", "herdr")
    version = subprocess.check_output([binary, "--version"], text=True).strip()
    if version != "herdr 0.9.5":
        raise RuntimeError("This fixture requires the pinned Herdr 0.9.5")
    with tempfile.TemporaryDirectory(prefix="novascale-herdr-test-", dir="/tmp") as directory:
        root = Path(directory)
        environment = os.environ.copy()
        environment.update(XDG_CONFIG_HOME=str(root / "config"), XDG_STATE_HOME=str(root / "state"), HERDR_CONFIG_PATH=str(root / "config.toml"), SHELL="/bin/sh")
        for name in ["HERDR_SOCKET_PATH", "HERDR_SESSION", "HERDR_SESSION_NAME", "SSH_AUTH_SOCK", "SSH_AGENT_PID"]:
            environment.pop(name, None)
        (root / "config.toml").write_text("")
        servers = []
        try:
            for name in ["push-test-a", "push-test-b"]:
                log = open(root / (name + ".log"), "wb")
                process = subprocess.Popen([binary, "--session", name, "server"], env=environment, stdin=subprocess.DEVNULL, stdout=log, stderr=log, cwd=root)
                path = root / "config" / "herdr" / "sessions" / name / "herdr.sock"
                servers.append((process, path, log))
                deadline = time.monotonic() + 15
                while not path.exists() and process.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.05)
                if not path.exists():
                    raise RuntimeError("Disposable server did not start")
                request(path, "ping", {})
            capture = root / "hooks.jsonl"
            hook = root / "capture.py"
            hook.write_text(
                'import os,json\np=json.loads(os.environ["HERDR_PLUGIN_EVENT_JSON"])\nd=p["data"]\n'
                + 'with open(' + repr(str(capture)) + ',"a") as f: f.write(json.dumps({"trigger":os.environ["HERDR_PLUGIN_EVENT"],"event":p["event"],"dataType":d.get("type"),"paneId":d.get("pane_id"),"status":d.get("agent_status"),"socket":os.environ["HERDR_SOCKET_PATH"],"metadata":{key:d.get(key) for key in ["pane_id","workspace_id","agent_status","agent","title","display_agent","state_labels"]}})+"\\n")\n'
            )
            plugin = root / "plugin"
            plugin.mkdir()
            (plugin / "herdr-plugin.toml").write_text(
                'id = "novascale.test"\nname = "NovaScale disposable test"\nversion = "0.1.0"\nmin_herdr_version = "0.9.1"\n'
                + '[[events]]\non = "pane.agent_status_changed"\ncommand = [' + json.dumps(shutil.which("python3")) + ', ' + json.dumps(str(hook)) + ']\n'
            )
            request(servers[0][1], "plugin.link", {"path": str(plugin), "enabled": True})
            plugins = request(servers[1][1], "plugin.list", {})["plugins"]
            assert any(plugin["plugin_id"] == "novascale.test" for plugin in plugins)
            sequences = []
            for _, path, _ in servers:
                created = request(path, "workspace.create", {"cwd": str(root), "focus": True, "label": "Disposable push test"})
                pane = created["root_pane"]["pane_id"]
                request(path, "pane.report_metadata", {"pane_id": pane, "source": "novascale-test",
                    "title": "Review migration", "display_agent": "Test Agent",
                    "state_labels": {"blocked": "Approve migration", "idle": "Ready for review"}, "seq": 1})
                for sequence, state in enumerate(["working", "blocked", "working", "idle"], 1):
                    request(path, "pane.report_agent", {"pane_id": pane, "source": "novascale-test", "agent": "codex", "state": state, "seq": sequence})
                    time.sleep(0.2)
                    agent = request(path, "agent.get", {"target": pane})["agent"]
                    assert agent["terminal_id"] and agent["pane_id"] == pane
                    sequences.append(agent["state_change_seq"])
                before = request(path, "agent.get", {"target": pane})["agent"]
                request(path, "pane.report_metadata", {"pane_id": pane, "source": "novascale-test", "title": "Private test title", "seq": 100})
                time.sleep(0.2)
                after = request(path, "agent.get", {"target": pane})["agent"]
                assert before["state_change_seq"] == after["state_change_seq"]
            deadline = time.monotonic() + 3
            while not capture.exists() and time.monotonic() < deadline:
                time.sleep(0.05)
            events = [json.loads(line) for line in capture.read_text().splitlines()]
            assert len({event["socket"] for event in events}) == 2
            assert all(event["trigger"] == "pane.agent_status_changed" and event["event"] == "pane_agent_status_changed" and event["dataType"] == "pane_agent_status_changed" for event in events)
            rich_events = [event["metadata"] for event in events if event["status"] in ["blocked", "idle"]
                           and event["metadata"]["title"] == "Review migration" and event["metadata"]["agent"] == "codex"]
            assert len(rich_events) == 4
            assert all(item["pane_id"] and item["workspace_id"] and item["agent"] == "codex"
                       and item["title"] == "Review migration" and item["display_agent"] == "Test Agent"
                       and item["state_labels"]["blocked"] == "Approve migration" for item in rich_events)
            assert all(later > earlier for earlier, later in zip(sequences[:3], sequences[1:4]))
            print(json.dumps({"herdrVersion": version, "detachedServers": 2, "crossSessionPluginReload": True, "hookCount": len(events), "typedStateSequence": True, "presentationPreservesSequence": True, "sevenDocumentedMetadataFields": True}))
        finally:
            for process, path, log in servers:
                try:
                    request(path, "server.stop", {})
                except (OSError, RuntimeError, ValueError):
                    process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                log.close()


if __name__ == "__main__":
    main()
