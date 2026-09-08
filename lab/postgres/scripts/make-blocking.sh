#!/usr/bin/env bash
# 作用：用超级用户开两个会话，制造可被 get_blocking_tree 看到的锁等待（约 90 秒）
# 运行主机：本机（docker exec 进实验室容器）
# 调用方：人工，可选
# 勿对生产库执行。
set -euo pipefail
CONTAINER="${POSTGRES_CONTAINER_NAME:-postgres-mcp-lab}"
docker exec -d "$CONTAINER" psql -U postgres -d mcp_lab -v ON_ERROR_STOP=1 \
  -c "BEGIN; UPDATE chaos.orders SET note = note || '-hold' WHERE id = 1; SELECT pg_sleep(90); COMMIT;"
sleep 1
echo "blocker_started; waiter will block until sleep ends or the container restarts"
docker exec "$CONTAINER" psql -U postgres -d mcp_lab -v ON_ERROR_STOP=1 \
  -c "BEGIN; UPDATE chaos.orders SET note = note || '-wait' WHERE id = 1; COMMIT;"
echo "waiter_done"
