#!/usr/bin/env bash
# 作用：把实验室 targets / pgpass 安装到本机，不覆盖已有生产清单
# 运行主机：本机
# 调用方：人工（lab/postgres 联调）
# 大概流程：
#   1) 复制 targets.lab.json → ~/.cursor/postgres-targets.lab.json
#   2) 若 ~/.pgpass 没有 mcp_lab 行则追加，chmod 600
# 勿放密钥：只复制仓库里标明的实验室口令
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "${HOME}/.cursor"
DEST="${HOME}/.cursor/postgres-targets.lab.json"
cp "$ROOT/targets.lab.json" "$DEST"
echo "targets=$DEST"

LINE="$(grep -v '^#' "$ROOT/pgpass.example" | grep -v '^[[:space:]]*$' | head -n1)"
PGPASS="${PGPASSFILE:-$HOME/.pgpass}"
touch "$PGPASS"
if grep -q '127.0.0.1:5432:mcp_lab:mcp_ro:' "$PGPASS" 2>/dev/null; then
  echo "pgpass_already_has_mcp_lab=$PGPASS"
else
  echo "$LINE" >> "$PGPASS"
  echo "pgpass_appended=$PGPASS"
fi
chmod 600 "$PGPASS"

echo
echo "Next: set PG_TARGETS_FILE in mcp.json to: $DEST"
echo "Then reload the postgres MCP."
