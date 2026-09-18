-- 作用：创建 MCP 只读用户。口令必须与 lab/mysql/mysqlpass.example、.env.example 的 MCP_RO_PASSWORD 一致。
-- 运行主机：容器内，仅 docker-entrypoint 首次初始化时
-- 勿把此口令用于生产。

CREATE USER IF NOT EXISTS 'mcp_ro'@'%' IDENTIFIED BY 'mcp-lab-readonly';

GRANT PROCESS, REPLICATION CLIENT, SHOW DATABASES, SHOW VIEW ON *.* TO 'mcp_ro'@'%';
GRANT SELECT ON performance_schema.* TO 'mcp_ro'@'%';
GRANT SELECT ON sys.* TO 'mcp_ro'@'%';
GRANT SELECT ON mcp_lab.* TO 'mcp_ro'@'%';

FLUSH PRIVILEGES;
