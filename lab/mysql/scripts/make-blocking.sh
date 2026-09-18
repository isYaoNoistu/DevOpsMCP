#!/usr/bin/env bash
# 作用：在实验室 MySQL 上制造一行 InnoDB 锁等待，方便验收 get_blocking_tree
set -euo pipefail
CONTAINER="${MYSQL_CONTAINER_NAME:-mysql-mcp-lab}"
ROOT_PASS="${MYSQL_ROOT_PASSWORD:-mcp-lab-super}"
echo "holding lock on chaos_orders.id=1 in ${CONTAINER} for ~25s..."
docker exec -d "$CONTAINER" mysql -uroot -p"${ROOT_PASS}" mcp_lab -e "START TRANSACTION; UPDATE chaos_orders SET note='blocked-hold' WHERE id=1; SELECT SLEEP(25); ROLLBACK;"
sleep 2
echo "second session will wait on the same row (timeout ~8s)..."
docker exec "$CONTAINER" mysql -uroot -p"${ROOT_PASS}" mcp_lab --connect-timeout=3 -e "SET innodb_lock_wait_timeout=8; START TRANSACTION; UPDATE chaos_orders SET note='blocked-wait' WHERE id=1;"
echo "done. If you still see a wait, call get_blocking_tree now."
