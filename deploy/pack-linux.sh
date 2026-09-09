#!/usr/bin/env bash
# 作用：默认 Linux 打包。编三个 ELF，带无密钥样例，打成 tar.gz
# 运行主机：开发机 / CI / 部署机（Linux、macOS、Git Bash）。有 Go 本机编，否则 docker golang
# 调用方：人工或 CI。接到月弦请用 attach.sh，不要用本包当容器挂载的唯一入口（也可以 --outdir 指到挂载点）
# 大概流程：
# 1) compile.py --os linux --arch amd64
# 2) 写入 deploy/dist/devopsmcp-linux-<arch>/
# 3) 打 tar.gz（可用 --no-archive 只要目录）
# 勿放密钥：包内只有 examples 占位符，不要把 .env / pgpass 拷进去

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCH="${GOARCH:-amd64}"
NO_ARCHIVE=0
OUT=""

usage() {
  cat <<'EOF'
Usage: ./pack-linux.sh [--arch amd64|arm64] [--outdir DIR] [--no-archive] [--docker]

  default outdir: deploy/dist/devopsmcp-linux-<arch>/
  archive:        deploy/dist/devopsmcp-linux-<arch>.tar.gz
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch ) ARCH="$2"; shift 2 ;;
    --outdir ) OUT="$2"; shift 2 ;;
    --no-archive ) NO_ARCHIVE=1; shift ;;
    --docker ) export PACK_FORCE_DOCKER=1; shift ;;
    -h|--help ) usage; exit 0 ;;
    * ) usage; exit 2 ;;
  esac
done

PYTHON="$(command -v python3 || command -v python || true)"
if [[ -z "$PYTHON" ]]; then
  echo "need python3" >&2
  exit 1
fi

DIST="${HERE}/dist"
mkdir -p "$DIST"
if [[ -z "$OUT" ]]; then
  OUT="${DIST}/devopsmcp-linux-${ARCH}"
fi
mkdir -p "$OUT"

EXTRA=()
[[ "${PACK_FORCE_DOCKER:-}" == "1" ]] && EXTRA+=(--docker)

"$PYTHON" "${HERE}/compile.py" --os linux --arch "$ARCH" --out "$OUT" "${EXTRA[@]}"

if [[ "$NO_ARCHIVE" -eq 1 ]]; then
  echo "linux pack dir: $OUT"
  exit 0
fi

ARCHIVE="${DIST}/devopsmcp-linux-${ARCH}.tar.gz"
tar -C "$(dirname "$OUT")" -czf "$ARCHIVE" "$(basename "$OUT")"
echo "linux pack dir: $OUT"
echo "linux archive : $ARCHIVE"
