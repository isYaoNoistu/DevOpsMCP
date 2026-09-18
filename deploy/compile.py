#!/usr/bin/env python3
"""Compile the DevOpsMCP stdio binaries (Linux ELF or Windows exe).

Used only by the no-argument pack scripts. Packages are written to deploy/dist.
"""

from __future__ import annotations

import json
import hashlib
import tempfile
import tarfile
import os
import shutil
import stat
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent

MODULES = (
    # (module dir relative to repo, binary stem, go build args inside that module)
    ("n9e-mcp-server", "n9e-mcp-server", ["./cmd/n9e-mcp-server/"]),
    ("jenkins-mcp-server", "jenkins-mcp-server", ["."]),
    ("postgres-mcp-server", "postgres-mcp-server", ["."]),
    ("mysql-mcp-server", "mysql-mcp-server", ["."]),
    ("host-logs-mcp-server", "host-logs-mcp-server", ["."]),
    ("kafka-mcp-server", "kafka-mcp-server", ["."]),
)

PACK_TXT = """Six read-only stdio MCP servers: Nightingale, Jenkins, PostgreSQL, MySQL, host logs, Kafka.
Configure your MCP client using examples/mcp.json.example. Replace absolute paths.
Fill connection details in private local files. Kafka uses KAFKA_TARGETS_FILE.
This package does not install, mount, register or start services.
"""


def env_get(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def binary_name(stem: str, goos: str) -> str:
    return f"{stem}.exe" if goos == "windows" else stem


def have_go() -> bool:
    return shutil.which("go") is not None


def have_docker() -> bool:
    return shutil.which("docker") is not None


def run(cmd: list[str], cwd: Path | None = None, env: dict[str, str] | None = None) -> None:
    print("+", " ".join(cmd), file=sys.stderr)
    subprocess.check_call(cmd, cwd=str(cwd) if cwd else None, env=env)


def native_env(goos: str, goarch: str) -> dict[str, str]:
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = goos
    env["GOARCH"] = goarch
    if env_get("GOPROXY"):
        env["GOPROXY"] = env_get("GOPROXY")
    return env


def compile_native(out: Path, goos: str, goarch: str) -> None:
    env = native_env(goos, goarch)
    for pkg, stem, src_args in MODULES:
        dest = out / binary_name(stem, goos)
        run(["go", "build", "-o", str(dest), *src_args], cwd=REPO / pkg, env=env)
        print(f"built {dest}", file=sys.stderr)


def compile_docker(out: Path, goos: str, goarch: str) -> None:
    image = env_get("GO_IMAGE", "golang:1.26-bookworm") or "golang:1.26-bookworm"
    proxy = env_get("GOPROXY", "https://proxy.golang.org,direct")
    out.mkdir(parents=True, exist_ok=True)
    # bash -l 会读 Debian /etc/profile，把镜像 ENV 里的 /usr/local/go/bin 从 PATH 清掉。
    inner = """
set -euo pipefail
export PATH="/usr/local/go/bin:/go/bin:${{PATH:-/usr/bin:/bin}}"
command -v go >/dev/null
cd /src/n9e-mcp-server && go build -o /out/{n9e} ./cmd/n9e-mcp-server/
cd /src/jenkins-mcp-server && go build -o /out/{jenkins} .
cd /src/postgres-mcp-server && go build -o /out/{pg} .
cd /src/mysql-mcp-server && go build -o /out/{mysql} .
cd /src/host-logs-mcp-server && go build -o /out/{hostlogs} .
cd /src/kafka-mcp-server && go build -o /out/{kafka} .
""".format(
        n9e=binary_name("n9e-mcp-server", goos),
        jenkins=binary_name("jenkins-mcp-server", goos),
        pg=binary_name("postgres-mcp-server", goos),
        mysql=binary_name("mysql-mcp-server", goos),
        hostlogs=binary_name("host-logs-mcp-server", goos),
        kafka=binary_name("kafka-mcp-server", goos),
    )
    cmd = [
        "docker",
        "run",
        "--rm",
        "-e",
        "CGO_ENABLED=0",
        "-e",
        f"GOOS={goos}",
        "-e",
        f"GOARCH={goarch}",
        "-e",
        f"GOPROXY={proxy}",
        "-v",
        f"{REPO}:/src:ro",
        "-v",
        f"{out}:/out",
        "-w",
        "/src",
        image,
        "bash",
        "-c",
        inner,
    ]
    run(cmd)
    print(f"built via {image} -> {out}", file=sys.stderr)


def copy_examples(out: Path, goos: str) -> None:
    dest = out / "examples"
    dest.mkdir(parents=True, exist_ok=True)
    files = [
        REPO / "examples" / "mcp.json.example",
        REPO / "examples" / "codex.toml.example",
        REPO / "postgres-mcp-server" / "examples" / "postgres-targets.example.json",
        REPO / "mysql-mcp-server" / "examples" / "mysql-targets.example.json",
        REPO / "host-logs-mcp-server" / "examples" / "host-logs-targets.example.json",
        REPO / "host-logs-mcp-server" / "examples" / "host-logs-targets.key.example.json",
        REPO / "host-logs-mcp-server" / "examples" / "host-logs-targets.multi-env.example.json",
    ]
    for src in files:
        if src.is_file():
            shutil.copy2(src, dest / src.name)
    kafka_examples = REPO / "kafka-mcp-server" / "examples"
    for name in ("targets.dev.json", "targets.tls-scram.json", "tool-calls.json"):
        shutil.copy2(kafka_examples / name, dest / ("kafka-" + name))
    config_path = dest / "mcp.json.example"
    config = json.loads(config_path.read_text(encoding="utf-8"))
    config["mcpServers"]["kafka"] = {
        "type": "stdio", "command": "/absolute/path/kafka-mcp-server", "args": [],
        "env": {"KAFKA_TARGETS_FILE": "/absolute/path/kafka-targets.json", "KAFKA_MCP_READ_ONLY": "true"}}
    toml = ["# Replace executable paths and private connection settings before use.\n"]
    for service, settings in config["mcpServers"].items():
        stem = "n9e-mcp-server" if service == "nightingale" else service + "-mcp-server"
        settings["command"] = "/absolute/path/" + binary_name(stem, goos)
        toml += [f"\n[mcp_servers.{service}]", "command = " + json.dumps(settings["command"]),
                 "args = " + json.dumps(settings.get("args", [])), f"[mcp_servers.{service}.env]"]
        toml += [key + " = " + json.dumps(value, ensure_ascii=False) for key, value in settings.get("env", {}).items()]
    config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (dest / "codex.toml.example").write_text("\n".join(toml) + "\n", encoding="utf-8")
    for module, _, _ in MODULES:
        docs = out / "docs" / module
        docs.mkdir(parents=True)
        for name in ("README.md", "LICENSE", "NOTICE", "VALIDATION.md"):
            src = REPO / module / name
            if src.is_file():
                shutil.copy2(src, docs / name)
        licenses = REPO / module / "licenses"
        if licenses.is_dir():
            shutil.copytree(licenses, docs / "licenses")
    for name in ("LICENSE", "NOTICE"):
        shutil.copy2(REPO / name, out / name)
    (out / "PACK.txt").write_text(PACK_TXT, encoding="utf-8")


def chmod_bins(out: Path, goos: str) -> None:
    if goos == "windows":
        return
    for _pkg, stem, _src in MODULES:
        path = out / binary_name(stem, goos)
        if path.is_file():
            path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def compile(out: Path, goos: str, goarch: str) -> None:
    out.mkdir(parents=True, exist_ok=True)
    use_docker = not have_go()
    if use_docker:
        if not have_docker():
            raise SystemExit("need Go 1.26+ or Docker to compile")
        compile_docker(out, goos, goarch)
    else:
        compile_native(out, goos, goarch)
    chmod_bins(out, goos)
    copy_examples(out, goos)


def main() -> int:
    if len(sys.argv) != 2 or sys.argv[1] not in ("linux", "windows"):
        raise SystemExit("Use pack-linux.sh or pack-windows.cmd / .ps1 / .sh without arguments")
    goos = sys.argv[1]
    dist = HERE / "dist"
    dist.mkdir(exist_ok=True)
    name = f"devopsmcp-{goos}-amd64"
    # Fresh staging prevents credentials or stale files in an old output entering the archive.
    with tempfile.TemporaryDirectory(prefix=".pack-", dir=dist) as temporary:
        out = Path(temporary) / name
        compile(out, goos, "amd64")
        hashes = {binary_name(stem, goos): hashlib.sha256((out / binary_name(stem, goos)).read_bytes()).hexdigest()
                  for _, stem, _ in MODULES}
        (out / "SHA256SUMS").write_text("".join(f"{digest}  {name}\n" for name, digest in hashes.items()), encoding="utf-8")
        if goos == "linux":
            archive = Path(temporary) / (name + ".tar.gz")
            def permissions(info):
                info.mode = 0o755 if info.isdir() or info.name in {name + "/" + stem for _, stem, _ in MODULES} else 0o644
                return info
            with tarfile.open(archive, "w:gz") as bundle:
                bundle.add(out, arcname=name, filter=permissions)
        else:
            archive = Path(shutil.make_archive(str(Path(temporary) / name), "zip", root_dir=temporary, base_dir=name))
        os.replace(archive, dist / archive.name)
        print(f"Package: {dist / archive.name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
