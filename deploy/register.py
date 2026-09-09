#!/usr/bin/env python3
"""Locate YLune, write Postgres sidecar files, upsert STDIO servers via the hub API.

Secrets stay in deploy/.env or the YLune deploy/.env. This process must not print them.
"""

from __future__ import annotations

import argparse
import json
import os
import stat
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent
REPO = HERE.parent
CONTAINER_BIN = "/opt/mcp"
DEFAULT_MOUNT = "/data/ylune-mcp"
DEFAULT_CONTAINER = "ylune"


def truthy(value: str | None) -> bool:
    return str(value or "").strip().lower() in {"1", "true", "yes", "on"}


def env_get(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def read_env_file(path: Path) -> dict[str, str]:
    out: dict[str, str] = {}
    if not path.is_file():
        return out
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip().lstrip("\ufeff").rstrip("\r")
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
            value = value[1:-1]
        out[key] = value
    return out


def docker_inspect(container: str) -> dict[str, Any] | None:
    try:
        raw = subprocess.check_output(
            ["docker", "inspect", container],
            stderr=subprocess.DEVNULL,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return None
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        return None
    if not data:
        return None
    return data[0]


def mount_from_inspect(info: dict[str, Any]) -> str:
    mounts = info.get("Mounts") or []
    preferred = ("/opt/mcp", "/opt/devopsmcp")
    by_dest = {m.get("Destination"): m.get("Source") or "" for m in mounts if isinstance(m, dict)}
    for dest in preferred:
        src = by_dest.get(dest)
        if src:
            return src
    return ""


def compose_dir_from_inspect(info: dict[str, Any]) -> Path | None:
    labels = (info.get("Config") or {}).get("Labels") or {}
    workdir = labels.get("com.docker.compose.project.working_dir") or ""
    if workdir:
        path = Path(workdir)
        if (path / "docker-compose.yml").is_file():
            return path
        if path.name == "deploy" and (path / "docker-compose.yml").is_file():
            return path
    return None


def candidate_ylune_homes() -> list[Path]:
    homes: list[Path] = []
    explicit = env_get("YLUNE_HOME")
    if explicit:
        homes.append(Path(explicit))
    homes.extend(
        [
            Path("/data/YLuneMCPHub"),
            REPO.parent / "YLuneMCPHub",
            Path.cwd().parent / "YLuneMCPHub",
        ]
    )
    seen: set[Path] = set()
    unique: list[Path] = []
    for home in homes:
        try:
            resolved = home.resolve()
        except OSError:
            continue
        if resolved in seen:
            continue
        seen.add(resolved)
        unique.append(home)
    return unique


def find_ylune_home(inspect_info: dict[str, Any] | None) -> Path | None:
    if inspect_info:
        compose_dir = compose_dir_from_inspect(inspect_info)
        if compose_dir:
            parent = compose_dir.parent
            if (parent / "Dockerfile").is_file() or (compose_dir / "docker-compose.yml").is_file():
                return parent if (parent / "Dockerfile").is_file() else compose_dir.parent
    for home in candidate_ylune_homes():
        if (home / "deploy" / "docker-compose.yml").is_file():
            return home
    return None


class Discovery:
    def __init__(self) -> None:
        self.container = env_get("YLUNE_CONTAINER", DEFAULT_CONTAINER)
        self.inspect = docker_inspect(self.container)
        self.home = find_ylune_home(self.inspect)
        self.ylune_env = read_env_file(self.home / "deploy" / ".env") if self.home else {}
        self.mount = self._resolve_mount()
        self.base_path = env_get("YLUNE_BASE_PATH") or self.ylune_env.get("BASE_PATH", "")
        port = env_get("YLUNE_PORT") or self.ylune_env.get("YLUNE_PORT", "3000") or "3000"
        self.url = env_get("YLUNE_URL") or f"http://127.0.0.1:{port}"
        self.user = env_get("YLUNE_USER") or "admin"
        self.password = env_get("YLUNE_PASSWORD") or self.ylune_env.get("ADMIN_PASSWORD", "")

    def _resolve_mount(self) -> Path:
        explicit = env_get("MCP_MOUNT_DIR")
        if explicit:
            return Path(explicit)
        if self.inspect:
            src = mount_from_inspect(self.inspect)
            if src:
                return Path(src)
        from_file = self.ylune_env.get("MCP_MOUNT_DIR", "").strip()
        if from_file:
            return Path(from_file)
        return Path(DEFAULT_MOUNT)

    def api_root(self) -> str:
        base = self.url.rstrip("/")
        prefix = (self.base_path or "").strip()
        if prefix and prefix != "/":
            if not prefix.startswith("/"):
                prefix = "/" + prefix
            base += prefix.rstrip("/")
        return base


def print_mount() -> int:
    disc = Discovery()
    print(disc.mount)
    return 0


def print_discover() -> int:
    disc = Discovery()
    payload = {
        "mount": str(disc.mount),
        "ylune_home": str(disc.home) if disc.home else "",
        "container": disc.container,
        "container_running": bool(disc.inspect),
        "url": disc.url,
        "base_path": disc.base_path,
        "opt_mcp_mounted": bool(disc.inspect and mount_from_inspect(disc.inspect)),
    }
    json.dump(payload, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")
    return 0


def write_pg_files(mount: Path, overwrite: bool) -> None:
    mount.mkdir(parents=True, exist_ok=True)
    try:
        os.chmod(mount, 0o700)
    except OSError:
        pass

    targets_path = mount / "postgres-targets.json"
    pgpass_path = mount / "pgpass"

    name = env_get("PG_TARGET_NAME", "app") or "app"
    host = env_get("PG_HOST", "host.docker.internal") or "host.docker.internal"
    port = int(env_get("PG_PORT", "5432") or "5432")
    dbname = env_get("PG_DBNAME", "yourdb") or "yourdb"
    user = env_get("PG_USER", "mcp_ro") or "mcp_ro"
    sslmode = env_get("PG_SSLMODE", "disable") or "disable"
    password = env_get("PG_PASSWORD")

    if overwrite or not targets_path.is_file():
        payload = {
            "targets": [
                {
                    "name": name,
                    "aliases": ["default"],
                    "description": "Generated by DevOpsMCP deploy/attach.sh",
                    "environment": "dev",
                    "host": host,
                    "port": port,
                    "dbname": dbname,
                    "user": user,
                    "sslmode": sslmode,
                    "tags": ["generated"],
                }
            ]
        }
        targets_path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"wrote {targets_path}", file=sys.stderr)

    if not password:
        if not pgpass_path.is_file():
            print("PG_PASSWORD empty; skip pgpass (create it before registering postgres)", file=sys.stderr)
        return

    if overwrite or not pgpass_path.is_file():
        line = f"{host}:{port}:{dbname}:{user}:{password}\n"
        pgpass_path.write_text(line, encoding="utf-8")
        os.chmod(pgpass_path, stat.S_IRUSR | stat.S_IWUSR)
        print(f"wrote {pgpass_path} (mode 600)", file=sys.stderr)


class HubClient:
    def __init__(self, disc: Discovery) -> None:
        self.disc = disc
        self.token = ""

    def _request(self, method: str, path: str, body: dict[str, Any] | None = None) -> Any:
        url = f"{self.disc.api_root()}{path}"
        data = None
        headers = {"Accept": "application/json"}
        if self.token:
            headers["x-auth-token"] = self.token
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                raw = resp.read().decode("utf-8")
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            message = detail
            try:
                parsed = json.loads(detail)
                message = str(parsed.get("message") or detail)
            except json.JSONDecodeError:
                pass
            raise RuntimeError(f"{method} {path} -> HTTP {exc.code}: {message}") from None
        except urllib.error.URLError as exc:
            raise RuntimeError(f"cannot reach YLune at {self.disc.api_root()}: {exc.reason}") from None
        if not raw:
            return {}
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return {"raw": raw}

    def login(self) -> None:
        if not self.disc.password:
            raise RuntimeError(
                "no YLune password: set YLUNE_PASSWORD in DevOpsMCP/deploy/.env "
                "(or keep the first-boot ADMIN_PASSWORD in YLune deploy/.env)"
            )
        resp = self._request(
            "POST",
            "/api/auth/login",
            {"username": self.disc.user, "password": self.disc.password},
        )
        token = resp.get("token") if isinstance(resp, dict) else None
        if not token:
            raise RuntimeError("login succeeded without a token")
        self.token = token

    def existing_names(self) -> set[str]:
        resp = self._request("GET", "/api/servers?limit=1000")
        data = resp.get("data") if isinstance(resp, dict) else None
        names: set[str] = set()
        if isinstance(data, list):
            for item in data:
                if isinstance(item, dict) and item.get("name"):
                    names.add(str(item["name"]))
        return names

    def upsert(self, name: str, config: dict[str, Any], existing: set[str]) -> None:
        if name in existing:
            self._request("PUT", f"/api/servers/{name}", {"config": config})
            self._request("POST", f"/api/servers/{name}/reload", {})
            print(f"updated {name}", file=sys.stderr)
            return
        try:
            self._request("POST", "/api/servers", {"name": name, "config": config})
            print(f"created {name}", file=sys.stderr)
        except RuntimeError as exc:
            if "already" not in str(exc).lower():
                raise
            self._request("PUT", f"/api/servers/{name}", {"config": config})
            self._request("POST", f"/api/servers/{name}/reload", {})
            print(f"updated {name}", file=sys.stderr)


def stdio_config(command: str, args: list[str] | None, env: dict[str, str], comment: str) -> dict[str, Any]:
    config: dict[str, Any] = {
        "type": "stdio",
        "command": command,
        "enabled": True,
        "visibility": "private",
        "description": comment,
        "env": env,
    }
    if args:
        config["args"] = args
    return config


def skip(reason: str) -> None:
    print(f"skip: {reason}", file=sys.stderr)


def register() -> int:
    disc = Discovery()
    if disc.inspect and not mount_from_inspect(disc.inspect):
        print(
            f"container {disc.container} is up but /opt/mcp is not mounted. "
            "git pull YLune, set MCP_MOUNT_DIR, then: docker compose up -d",
            file=sys.stderr,
        )
        return 1

    client = HubClient(disc)
    client.login()
    existing = client.existing_names()
    bin_dir = CONTAINER_BIN

    if truthy(env_get("REGISTER_NIGHTINGALE", "true")):
        token = env_get("N9E_TOKEN")
        if not token:
            skip("REGISTER_NIGHTINGALE but N9E_TOKEN is empty")
        else:
            client.upsert(
                "nightingale",
                stdio_config(
                    f"{bin_dir}/n9e-mcp-server",
                    ["stdio"],
                    {
                        "N9E_TOKEN": token,
                        "N9E_BASE_URL": env_get("N9E_BASE_URL", "http://host.docker.internal:17000"),
                        "N9E_TOOLSETS": env_get(
                            "N9E_TOOLSETS",
                            "alerts,targets,datasource,busi_groups,metrics,logs",
                        ),
                        "N9E_READ_ONLY": "true",
                    },
                    "DevOpsMCP nightingale (stdio)",
                ),
                existing,
            )
            existing.add("nightingale")

    if truthy(env_get("REGISTER_JENKINS", "true")):
        token = env_get("JENKINS_API_TOKEN")
        if not token:
            skip("REGISTER_JENKINS but JENKINS_API_TOKEN is empty")
        else:
            client.upsert(
                "jenkins",
                stdio_config(
                    f"{bin_dir}/jenkins-mcp-server",
                    None,
                    {
                        "JENKINS_URL": env_get("JENKINS_URL", "http://host.docker.internal:8080"),
                        "JENKINS_USER": env_get("JENKINS_USER", "mcp-readonly"),
                        "JENKINS_API_TOKEN": token,
                        "JENKINS_MCP_TIMEOUT": env_get("JENKINS_MCP_TIMEOUT", "90s"),
                    },
                    "DevOpsMCP jenkins (stdio)",
                ),
                existing,
            )
            existing.add("jenkins")

    if truthy(env_get("REGISTER_POSTGRES", "false")):
        pgpass = disc.mount / "pgpass"
        targets = disc.mount / "postgres-targets.json"
        if not targets.is_file() or not pgpass.is_file():
            skip("REGISTER_POSTGRES but postgres-targets.json or pgpass is missing on the mount")
        else:
            client.upsert(
                "postgres",
                stdio_config(
                    f"{bin_dir}/postgres-mcp-server",
                    None,
                    {
                        "PG_TARGETS_FILE": f"{bin_dir}/postgres-targets.json",
                        "PGPASSFILE": f"{bin_dir}/pgpass",
                        "PG_MCP_READ_ONLY": "true",
                    },
                    "DevOpsMCP postgres (stdio)",
                ),
                existing,
            )

    print("register done. add users to groups in the YLune console; admins need no group.")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="YLune attach helpers for DevOpsMCP")
    parser.add_argument("--print-mount", action="store_true")
    parser.add_argument("--discover", action="store_true")
    parser.add_argument("--write-pg-files", action="store_true")
    parser.add_argument("--register", action="store_true")
    parser.add_argument("--overwrite-pg", action="store_true")
    args = parser.parse_args()

    if args.print_mount:
        return print_mount()
    if args.discover:
        return print_discover()
    if args.write_pg_files:
        disc = Discovery()
        write_pg_files(disc.mount, overwrite=args.overwrite_pg or truthy(env_get("OVERWRITE_PG_CONFIG")))
        return 0
    if args.register:
        return register()

    parser.print_help()
    return 2


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1) from None
