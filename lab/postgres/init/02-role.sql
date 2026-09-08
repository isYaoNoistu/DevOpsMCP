-- 作用：创建 MCP 只读角色。口令必须与 lab/postgres/pgpass.example、.env.example 的 MCP_RO_PASSWORD 一致。
-- 运行主机：容器内，仅 docker-entrypoint 首次初始化时
-- 勿把此口令用于生产。

CREATE ROLE mcp_ro LOGIN PASSWORD 'mcp-lab-readonly';

GRANT CONNECT ON DATABASE mcp_lab TO mcp_ro;
GRANT pg_read_all_data TO mcp_ro;
GRANT pg_monitor TO mcp_ro;

ALTER ROLE mcp_ro SET default_transaction_read_only = on;
ALTER ROLE mcp_ro SET statement_timeout = '10s';
ALTER ROLE mcp_ro SET lock_timeout = '2s';
ALTER ROLE mcp_ro SET idle_in_transaction_session_timeout = '10s';
