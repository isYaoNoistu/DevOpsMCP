#!/usr/bin/env python3
"""Compile the three DevOpsMCP stdio binaries (Linux ELF or Windows exe).

Used by pack-linux.sh, pack-windows.ps1 / pack-windows.sh, and attach.sh.
Does not print tokens. Does not wipe extra files already in --out (pgpass etc.).
"""

from __future__ import annotations

import argparse
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
)

PACK_TXT = """DevOpsMCP stdio binaries (read-only Nightingale / Jenkins / PostgreSQL)

This folder is a build output. Put it anywhere; do not commit it.

1. Copy examples/mcp.json.example
2. Change each command to the absolute path of the binary in THIS folder
   (Windows: the .exe files). Fill tokens in your local client config only.
3. PostgreSQL: copy examples/postgres-targets.example.json, remove any password
   field, point PG_TARGETS_FILE at that file. Put the password in pgpass /
   Credential Manager.

Same-host URLs for a Docker hub: host.docker.internal, not 127.0.0.1.

YLune attach (Linux host + Docker hub) is deploy/attach.sh, not this zip.
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
    image = env_get("GO_IMAGE", "golang:1.23-bookworm") or "golang:1.23-bookworm"
    proxy = env_get("GOPROXY", "https://proxy.golang.org,direct")
    out.mkdir(parents=True, exist_ok=True)
    inner = """
set -euo pipefail
cd /src/n9e-mcp-server && go build -o /out/{n9e} ./cmd/n9e-mcp-server/
cd /src/jenkins-mcp-server && go build -o /out/{jenkins} .
cd /src/postgres-mcp-server && go build -o /out/{pg} .
""".format(
        n9e=binary_name("n9e-mcp-server", goos),
        jenkins=binary_name("jenkins-mcp-server", goos),
        pg=binary_name("postgres-mcp-server", goos),
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
        "-lc",
        inner,
    ]
    run(cmd)
    print(f"built via {image} -> {out}", file=sys.stderr)


def copy_examples(out: Path) -> None:
    dest = out / "examples"
    dest.mkdir(parents=True, exist_ok=True)
    files = [
        REPO / "examples" / "mcp.json.example",
        REPO / "examples" / "codex.toml.example",
        REPO / "postgres-mcp-server" / "examples" / "postgres-targets.example.json",
    ]
    for src in files:
        if src.is_file():
            shutil.copy2(src, dest / src.name)
    (out / "PACK.txt").write_text(PACK_TXT, encoding="utf-8")


def chmod_bins(out: Path, goos: str) -> None:
    if goos == "windows":
        return
    for _pkg, stem, _src in MODULES:
        path = out / binary_name(stem, goos)
        if path.is_file():
            path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def compile(out: Path, goos: str, goarch: str, bins_only: bool, force_docker: bool) -> None:
    out.mkdir(parents=True, exist_ok=True)
    use_docker = force_docker or not have_go()
    if use_docker:
        if not have_docker():
            raise SystemExit("need Go 1.23+ or Docker to compile")
        compile_docker(out, goos, goarch)
    else:
        compile_native(out, goos, goarch)
    chmod_bins(out, goos)
    if not bins_only:
        copy_examples(out)


def main() -> int:
    parser = argparse.ArgumentParser(description="Compile DevOpsMCP binaries")
    parser.add_argument("--os", dest="goos", default="linux", choices=("linux", "windows", "darwin"))
    parser.add_argument("--arch", dest="goarch", default="amd64")
    parser.add_argument("--out", required=True, help="output directory (created, not wiped)")
    parser.add_argument("--bins-only", action="store_true", help="do not copy examples/PACK.txt")
    parser.add_argument("--docker", action="store_true", help="force golang image even if Go is installed")
    args = parser.parse_args()
    compile(Path(args.out).expanduser().resolve(), args.goos, args.goarch, args.bins_only, args.docker)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
