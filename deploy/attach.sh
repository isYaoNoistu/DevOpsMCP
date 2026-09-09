#!/usr/bin/env bash
# 作用：把本仓库编成 Linux 二进制，写入月弦挂载点 /opt/mcp，并按 .env 自动在月弦注册 STDIO 服务
# 运行主机：Linux 部署机（与月弦 Docker 同一台）。需要 bash、python3、docker（无本机 Go 时用 golang 镜像编）
# 调用方：人工。月弦先按它自己的 deploy/ 启动；本脚本不改月弦启动方式
# 大概流程：
# 1) 读 deploy/.env（选哪些服务、Token、可选 YLUNE_*）
# 2) 查找月弦：YLUNE_HOME → /data/YLuneMCPHub → 同级目录 → 容器 ylune 的 /opt/mcp 挂载
# 3) compile.py 向挂载目录写入三个 Linux ELF（本机 Go 或 docker golang）
# 4) 若开启 PostgreSQL：生成 postgres-targets.json + pgpass（已有则默认不覆盖）
# 5) 登录月弦 API，创建或更新 nightingale / jenkins / postgres
# 勿放密钥：Token 和口令只写 .env / pgpass，不要提交仓库、不要 echo

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE"

usage() {
  cat <<'EOF'
Usage: ./attach.sh [--build-only | --register-only | --discover]

  (default)     compile Linux binaries into the YLune mount, write PG files if enabled, register
  --build-only  compile only
  --register-only  register only (binaries and files must already be on the mount)
  --discover    print where YLune and the mount were found (no secrets)
EOF
}

MODE="all"
case "${1:-}" in
  "" ) MODE="all" ;;
  --build-only ) MODE="build" ;;
  --register-only ) MODE="register" ;;
  --discover ) MODE="discover" ;;
  -h|--help ) usage; exit 0 ;;
  * ) usage; exit 2 ;;
esac

if [[ -f "${HERE}/.env" ]]; then
  set -a
  # strip CR so Windows-edited .env still sources on Linux
  # shellcheck disable=SC1090
  source <(sed 's/\r$//' "${HERE}/.env")
  set +a
else
  echo "missing ${HERE}/.env — copy .env.example and fill tokens" >&2
  exit 1
fi

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "need command: $1" >&2
    exit 1
  }
}

PYTHON="$(command -v python3 || command -v python || true)"
if [[ -z "$PYTHON" ]]; then
  echo "need python3 or python" >&2
  exit 1
fi

if [[ "$MODE" == "discover" ]]; then
  "$PYTHON" "${HERE}/register.py" --discover
  exit 0
fi

MOUNT="$("$PYTHON" "${HERE}/register.py" --print-mount)"
if [[ -z "$MOUNT" ]]; then
  echo "could not resolve MCP mount directory" >&2
  exit 1
fi

echo "mount (host) : ${MOUNT}"
echo "mount (hub)  : /opt/mcp"
mkdir -p "$MOUNT"

is_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES|on|ON ) return 0 ;;
    * ) return 1 ;;
  esac
}

build_all() {
  "$PYTHON" "${HERE}/compile.py" --os linux --arch "${GOARCH:-amd64}" --out "$MOUNT" --bins-only
  if command -v file >/dev/null 2>&1; then
    file "${MOUNT}/jenkins-mcp-server" || true
  fi
}

write_pg() {
  is_true "${REGISTER_POSTGRES:-false}" || return 0
  local extra=()
  is_true "${OVERWRITE_PG_CONFIG:-false}" && extra+=(--overwrite-pg)
  "$PYTHON" "${HERE}/register.py" --write-pg-files "${extra[@]}"
}

register_all() {
  "$PYTHON" "${HERE}/register.py" --register
}

case "$MODE" in
  build )
    build_all
    write_pg
    ;;
  register )
    write_pg
    register_all
    ;;
  all )
    build_all
    write_pg
    register_all
    ;;
esac
