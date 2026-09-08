-- 作用：造一点可观测的表、未使用索引、以及可供阻塞演练的行
-- 运行主机：容器内，仅 docker-entrypoint 首次初始化时

CREATE SCHEMA IF NOT EXISTS chaos;

CREATE TABLE chaos.orders (
    id          bigint PRIMARY KEY,
    customer_id int NOT NULL,
    amount_cents int NOT NULL,
    note        text,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chaos.payments (
    id       bigint PRIMARY KEY,
    order_id bigint NOT NULL REFERENCES chaos.orders (id),
    status   text NOT NULL
);

INSERT INTO chaos.orders (id, customer_id, amount_cents, note)
SELECT g, (g % 50) + 1, 100 + (g % 900), 'note-' || g
FROM generate_series(1, 800) AS g;

INSERT INTO chaos.payments (id, order_id, status)
SELECT g, g, CASE WHEN g % 7 = 0 THEN 'failed' ELSE 'ok' END
FROM generate_series(1, 200) AS g;

-- 几乎用不上的索引，方便对照 get_index_stats 的 idx_scan
CREATE INDEX idx_orders_note_unused ON chaos.orders (note);

ANALYZE chaos.orders;
ANALYZE chaos.payments;

GRANT USAGE ON SCHEMA chaos TO mcp_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA chaos TO mcp_ro;
