# 作用：在实验室 MySQL 上制造一行 InnoDB 锁等待，方便验收 get_blocking_tree
# 运行主机：本机（docker exec 进 mysql-mcp-lab）
# 调用方：人工
# 大概流程：会话 A 更新 id=1 不提交；会话 B 再更新同一行并等待
# 勿放密钥：只用实验室 root 口令，不连生产

$ErrorActionPreference = "Stop"
$Container = if ($env:MYSQL_CONTAINER_NAME) { $env:MYSQL_CONTAINER_NAME } else { "mysql-mcp-lab" }
$RootPass = if ($env:MYSQL_ROOT_PASSWORD) { $env:MYSQL_ROOT_PASSWORD } else { "mcp-lab-super" }

Write-Output "holding lock on chaos_orders.id=1 in $Container for ~25s..."
docker exec -d $Container mysql -uroot "-p$RootPass" mcp_lab -e "START TRANSACTION; UPDATE chaos_orders SET note='blocked-hold' WHERE id=1; SELECT SLEEP(25); ROLLBACK;"
Start-Sleep -Seconds 2
Write-Output "second session will wait on the same row (timeout ~8s)..."
docker exec $Container mysql -uroot "-p$RootPass" mcp_lab --connect-timeout=3 -e "SET innodb_lock_wait_timeout=8; START TRANSACTION; UPDATE chaos_orders SET note='blocked-wait' WHERE id=1;"
Write-Output "done. If you still see a wait, call get_blocking_tree now."
