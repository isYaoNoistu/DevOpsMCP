#!/usr/bin/env bash
# 作用：把实验室 targets / mysqlpass 安装到本机，不覆盖已有生产清单
# 运行主机：本机
# 调用方：人工（lab/mysql 联调）
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST_DIR="${HOME}/.cursor"
mkdir -p "$DEST_DIR"
cp -f "${ROOT}/targets.lab.json" "${DEST_DIR}/mysql-targets.lab.json"
echo "targets=${DEST_DIR}/mysql-targets.lab.json"

PASSFILE="${HOME}/.mysqlpass"
LINE="$(grep -v '^#' "${ROOT}/mysqlpass.example" | grep -v '^[[:space:]]*$' | head -n1)"
if [[ -z "$LINE" ]]; then
  echo "mysqlpass.example has no data line" >&2
  exit 1
fi
if [[ -f "$PASSFILE" ]] && grep -q '127.0.0.1:3306:mcp_lab:mcp_ro:' "$PASSFILE"; then
  echo "mysqlpass_already_has_mcp_lab=${PASSFILE}"
else
  if [[ ! -f "$PASSFILE" ]]; then
    printf '%s\n' '# hostname:port:database:username:password' > "$PASSFILE"
  fi
  printf '%s\n' "$LINE" >> "$PASSFILE"
  chmod 600 "$PASSFILE"
  echo "mysqlpass_updated=${PASSFILE}"
fi
echo "Next: MYSQL_TARGETS_FILE=${DEST_DIR}/mysql-targets.lab.json"
