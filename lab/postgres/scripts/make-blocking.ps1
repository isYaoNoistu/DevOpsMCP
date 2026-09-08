# 作用：用超级用户开两个会话，制造可被 get_blocking_tree 看到的锁等待（约 90 秒）
# 运行主机：本机（docker exec 进实验室容器）
# 调用方：人工，可选
# 大概流程：
#   1) 会话 A：BEGIN 更新 id=1 后 pg_sleep
#   2) 会话 B：同样更新 id=1，进入 wait
# 勿放密钥：只连本地实验室容器。勿对生产库执行。

$ErrorActionPreference = "Stop"
$container = if ($env:POSTGRES_CONTAINER_NAME) { $env:POSTGRES_CONTAINER_NAME } else { "postgres-mcp-lab" }

docker exec -d $container psql -U postgres -d mcp_lab -v ON_ERROR_STOP=1 -c "BEGIN; UPDATE chaos.orders SET note = note || '-hold' WHERE id = 1; SELECT pg_sleep(90); COMMIT;"
Start-Sleep -Seconds 1
Write-Output "blocker_started; starting waiter (will block until sleep ends or you restart the container)"
docker exec $container psql -U postgres -d mcp_lab -v ON_ERROR_STOP=1 -c "BEGIN; UPDATE chaos.orders SET note = note || '-wait' WHERE id = 1; COMMIT;"
Write-Output "waiter_done"
