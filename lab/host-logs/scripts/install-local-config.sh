# 作用：把实验室 host-logs targets 安装到本机，写入夹具目录的绝对路径
# 运行主机：本机（Linux / macOS / Git Bash）
# 调用方：人工（lab/host-logs 联调）
# 大概流程：解析 fixtures 绝对路径，写 ~/.cursor/host-logs-targets.lab.json
# 勿放密钥：实验室无 SSH、无 password

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FIXTURES="$(cd "${ROOT}/fixtures" && pwd)"
DEST="${HOME}/.cursor/host-logs-targets.lab.json"
mkdir -p "${HOME}/.cursor"

python3 - "$DEST" "$FIXTURES" <<'PY'
import json, sys
dest, fixtures = sys.argv[1], sys.argv[2]
payload = {
    "targets": [
        {
            "name": "mcp-lab-host-logs",
            "aliases": ["实验室日志", "lab host logs"],
            "description": "DevOpsMCP lab/host-logs fixtures (local, no SSH)",
            "environment": "dev",
            "host": "local",
            "transport": "local",
            "paths": [fixtures],
            "tags": ["local", "lab", "postgres"],
        }
    ]
}
with open(dest, "w", encoding="utf-8") as f:
    json.dump(payload, f, ensure_ascii=False, indent=2)
    f.write("\n")
print("targets=" + dest)
print("fixtures=" + fixtures)
PY

echo
echo "Next: set HOST_LOGS_TARGETS_FILE in mcp.json to:"
echo "${DEST}"
echo "Then reload the host-logs MCP."
