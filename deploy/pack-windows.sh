#!/usr/bin/env bash
# 作用：默认 Windows 打包（可在 Linux/macOS 上交叉编译 .exe + zip）
# 运行主机：开发机 / CI。有 Go 则交叉编译；否则 docker golang + GOOS=windows
# 调用方：人工或 CI。Windows 本机更推荐 pack-windows.cmd / pack-windows.ps1
# 大概流程：compile.py --os windows → dist/devopsmcp-windows-<arch>/ → zip
# 勿放密钥：包内只有 examples 占位符

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCH="${GOARCH:-amd64}"
NO_ARCHIVE=0
OUT=""

usage() {
  cat <<'EOF'
Usage: ./pack-windows.sh [--arch amd64|arm64] [--outdir DIR] [--no-archive] [--docker]

  default outdir: deploy/dist/devopsmcp-windows-<arch>/
  archive:        deploy/dist/devopsmcp-windows-<arch>.zip
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
  OUT="${DIST}/devopsmcp-windows-${ARCH}"
fi
mkdir -p "$OUT"

EXTRA=()
[[ "${PACK_FORCE_DOCKER:-}" == "1" ]] && EXTRA+=(--docker)

"$PYTHON" "${HERE}/compile.py" --os windows --arch "$ARCH" --out "$OUT" "${EXTRA[@]}"

if [[ "$NO_ARCHIVE" -eq 1 ]]; then
  echo "windows pack dir: $OUT"
  exit 0
fi

ARCHIVE="${DIST}/devopsmcp-windows-${ARCH}.zip"
"$PYTHON" - "$OUT" "$ARCHIVE" <<'PY'
import sys, zipfile
from pathlib import Path
src, dest = Path(sys.argv[1]), Path(sys.argv[2])
dest.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(dest, "w", zipfile.ZIP_DEFLATED) as zf:
    for path in src.rglob("*"):
        if path.is_file():
            zf.write(path, path.relative_to(src.parent))
print(dest)
PY
echo "windows pack dir: $OUT"
echo "windows archive : $ARCHIVE"
