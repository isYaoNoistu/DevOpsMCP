"""Offline stdio smoke: no Kafka connection, credentials or persistent config."""
import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import queue
import subprocess
import threading
import time
import uuid


@contextmanager
def fixture_path(directory):
    # Exclusive file creation avoids Windows temporary-directory ACL differences.
    path = directory / ("kafka-mcp-smoke-" + uuid.uuid4().hex + ".json")
    with path.open("x", encoding="utf-8"):
        pass
    try:
        yield path
    finally:
        path.unlink()


def main():
    module = Path(__file__).resolve().parents[1]
    default = module.parent.parent / "dist/devopsmcp-dev-windows-amd64/kafka-mcp-server.exe"
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=default)
    args = parser.parse_args()
    expected = {"list_targets", "get_target_info", "kafka_capabilities",
                "kafka_configs_query", "kafka_topic_inspect", "kafka_group_inspect",
                "kafka_offsets_query", "kafka_records_peek",
                "kafka_topics_list", "kafka_groups_list"}
    version = subprocess.run([str(args.binary.resolve()), "--version"],
        check=True, capture_output=True, text=True, encoding="utf-8", timeout=10).stdout.strip()
    assert version, "--version returned no build identity"
    # Keep the temporary fixture beside the selected build, outside the repository.
    with fixture_path(args.binary.resolve().parent) as targets:
        targets.write_text(json.dumps({"targets": [{"name": "offline-fixture",
            "brokers": ["127.0.0.1:1"], "topics": ["demo-events"],
            "groups": ["demo-reader-*"], "allow_payload": False}]}), encoding="utf-8")
        env = dict(os.environ, KAFKA_TARGETS_FILE=str(targets), KAFKA_MCP_READ_ONLY="true")
        proc = subprocess.Popen([str(args.binary.resolve())], stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", env=env)
        lines = queue.Queue()
        def read_output():
            for line in proc.stdout:
                lines.put(line)
            lines.put(None)
        threading.Thread(target=read_output, daemon=True).start()
        def send(method, params, identifier=None):
            request = {"jsonrpc": "2.0", "method": method, "params": params}
            if identifier is not None:
                request["id"] = identifier
            proc.stdin.write(json.dumps(request) + "\n")
            proc.stdin.flush()
            if identifier is None:
                return None
            deadline = time.monotonic()+10
            while True:
                remaining = deadline-time.monotonic()
                if remaining <= 0:
                    raise TimeoutError("MCP response timeout")
                line = lines.get(timeout=remaining)
                if line is None:
                    raise RuntimeError("server exited before response")
                response = json.loads(line)
                if "id" not in response and response.get("method", "").startswith("notifications/"):
                    continue
                break
            if response.get("id") != identifier or "error" in response:
                raise RuntimeError("unexpected JSON-RPC response")
            return response["result"]
        try:
            init = send("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                "clientInfo": {"name": "offline-smoke", "version": "1"}}, 1)
            assert init["serverInfo"]["name"] == "kafka"
            send("notifications/initialized", {})
            listed = send("tools/list", {}, 2)
            assert {tool["name"] for tool in listed["tools"]} == expected
            assert all(tool["annotations"]["readOnlyHint"] for tool in listed["tools"])
            result = send("tools/call", {"name": "list_targets", "arguments": {}}, 3)
            assert not result.get("isError", False)
            envelope = json.loads(next(item["text"] for item in result["content"] if item["type"] == "text"))
            assert envelope["status"] == "ok"
            assert [target["name"] for target in envelope["data"]] == ["offline-fixture"]
            assert envelope["data"][0]["allow_payload"] is False
            print("PASS: --version, initialize, 10 read-only tools, local list_targets; no Kafka queried")
        finally:
            proc.stdin.close()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
            proc.stdout.close()


if __name__ == "__main__":
    main()
