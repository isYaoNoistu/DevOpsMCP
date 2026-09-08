-- 作用：实验室数据库启用 pg_stat_statements，供 list_slow_queries 使用
-- 运行主机：容器内，仅 docker-entrypoint 首次初始化时
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
