# PostgreSQL MCP 配置与凭据

本文说明三份本机文件怎么写、密码放哪、MCP 怎么找到密码。示例主机和口令都是占位或一次性实验室值，不要换成你们生产地址后提交回仓库。

相关文件：

| 文件 | 放哪 | 进 Git 吗 | 作用 |
| --- | --- | --- | --- |
| `mcp.json` | WorkBuddy：`~/.workbuddy/mcp.json`；Cursor：`~/.cursor/mcp.json`（其它见根 README） | 否 | 告诉客户端：二进制路径、**targets 文件路径**、只读开关 |
| `postgres-targets.json` | 任意本机路径，由 `PG_TARGETS_FILE` 指向 | 否 | 有哪些库：host / dbname / user / sslmode。**禁止 password** |
| 密码 | Windows 凭据管理器，或 `pgpass.conf` / `~/.pgpass` | 否 | 只给 `mcp_ro` 用，不进 targets、不进对话 |

样例（无真实环境）：[`examples/postgres-targets.example.json`](../examples/postgres-targets.example.json)、[`examples/mcp.json.example`](../../examples/mcp.json.example)。本机 Docker 联调见仓库 [`lab/postgres/`](../../lab/postgres/README.md)。

## 原理（先看这个）

MCP **不**在 `mcp.json` 里为每个库放一个环境变量密码，也 **不**在 targets JSON 里存口令。连接串是运行时拼出来的：

```text
MCP 客户端（WorkBuddy / Cursor / …）
  └─ 启动 postgres-mcp-server（stdio）
        ├─ 读 PG_TARGETS_FILE → 得到 name / host / port / dbname / user / sslmode / credential_ref
        ├─ 某次工具调用带 target=orders-prod
        ├─ 取密码（见下）
        └─ 对该 target 建连接池：BEGIN READ ONLY + statement_timeout / lock_timeout
              用完连接先 DISCARD ALL 再还池，避免会话级锁留在连接上
```

取密码顺序（`internal/cred`）：

1. 若 target 写了 `credential_ref`（例如 `postgres/orders-prod`）：
   - **Windows**：在凭据管理器里找 **Generic** 凭据，目标名必须与 `credential_ref` **完全一致**。
   - **Linux / macOS**：没有凭据管理器实现，这一步直接跳过。
2. 若上一步没有拿到非空密码：读 **pgpass**（libpq 同款格式）。
3. 两处都没有 → 报错 `no password for target ...`，**不会**回退去读环境变量 `PGPASSWORD`，也 **不会**用应用账号。

因此：targets 回答「连哪」；凭据库 / pgpass 回答「口令是什么」；`mcp.json` 只回答「清单文件在哪」。

改 targets 文件后，下一轮 `list_targets` 会按文件 mtime 重新加载，**不用**重载 MCP。改二进制路径、改 `mcp.json` 里的环境变量，仍要在所用客户端里重载。

## 1. 客户端：`mcp.json`

WorkBuddy 与 Cursor 用同一段 JSON（建议 `"type": "stdio"`）。完整客户端列表见仓库根 README。只配进程，不配密码：

```json
{
  "mcpServers": {
    "postgres": {
      "type": "stdio",
      "command": "/ABS/PATH/DevOpsMCP/postgres-mcp-server/postgres-mcp-server",
      "args": [],
      "env": {
        "PG_TARGETS_FILE": "/ABS/PATH/postgres-targets.json",
        "PG_MCP_READ_ONLY": "true"
      }
    }
  }
}
```

Windows 把 `command` 换成 `...\postgres-mcp-server.exe`。`PG_TARGETS_FILE` 必须是**绝对路径**。实验室联调建议单独一份清单（例如 `postgres-targets.lab.json`），不要覆盖你已经在用的生产清单。

可选环境变量（都有默认值）：

| 变量 | 默认 | 含义 |
| --- | --- | --- |
| `PG_MCP_READ_ONLY` | 必须为 `true` | 进程自己的约束；真正安全靠数据库角色 |
| `PG_MCP_STATEMENT_TIMEOUT` | `10s` | 单条查询超时 |
| `PG_MCP_LOCK_TIMEOUT` | `2s` | 等锁超时 |
| `PG_MCP_CONNECT_TIMEOUT` | `5s` | 连接超时 |
| `PG_MCP_ALLOW_EXPLAIN_ANALYZE` | 关 | 非生产上才允许 `explain_query` 的 `analyze=true`；生产 target 即使打开也拒绝 |
| `PGPASSFILE` | 见下一节默认路径 | 覆盖 pgpass 文件位置 |

`PG_MCP_READ_ONLY=true` **不能**代替只读账号。应用账号、SUPERUSER 不要配进 MCP。

## 2. 目标清单：`postgres-targets.json`

根对象只有一个数组 `targets`。每个元素：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `name` | 是 | 稳定机器 ID。其它工具都传这个（或能唯一命中的别名）。建议 `业务-环境`，如 `orders-prod` |
| `host` | 是 | 主机名或 IP |
| `dbname` | 是 | 数据库名 |
| `user` | 是 | 登录角色，建议 `mcp_ro` |
| `port` | 否 | 默认 `5432` |
| `sslmode` | 否 | 默认 `verify-full`。允许：`disable` / `allow` / `prefer` / `require` / `verify-ca` / `verify-full` |
| `credential_ref` | 建议 | Windows Generic 凭据的目标名。缺省时错误信息会提示 `postgres/<name>` |
| `aliases` | 否 | 中文名、口头名。冲突时 MCP 返回候选，不猜 |
| `description` | 否 | 给人看的说明 |
| `environment` | 否 | `prod` / `production` / `prd` 会按**生产**处理（见下） |
| `tags` | 否 | 同样，含 `prod` / `production` / `prd` 视为生产 |
| `password` | **禁止** | 只要出现非空字符串，加载失败 |

生产判定（任一成立即可）：`environment` 或某个 `tag` 为 prod/production/prd（大小写不敏感）；或 `name` 以 `-prod`、`_prod`、`-production`、`_production` 结尾。

生产 target **不允许** `sslmode` 为 `disable` / `allow` / `prefer`。本机 Docker、没有可信 CA 的非生产库请显式写 `disable` 或 `prefer`，并把 `environment` 设为 `dev`。

`name` 不要当显示名改来改去。人类可读的放 `aliases`。

最小可用例子（非生产、本机）：

```json
{
  "targets": [
    {
      "name": "mcp-lab-local",
      "aliases": ["本地测试库"],
      "environment": "dev",
      "host": "127.0.0.1",
      "port": 5432,
      "dbname": "mcp_lab",
      "user": "mcp_ro",
      "sslmode": "disable",
      "credential_ref": "postgres/mcp-lab-local",
      "tags": ["local", "lab"]
    }
  ]
}
```

JSON 坏了、`name` 重复、缺 host/dbname/user、生产还用了 `prefer`：工具返回错误，并保留上一份**成功**清单，不会假装已经切到新库。

## 3. 密码写在哪

### 3.1 Windows 凭据管理器（优先）

- 类型：**普通凭据 / Generic**（不是 Windows 域凭据）。
- **目标 Internet 或网络地址**（cmdkey 的 `/generic:`）：必须等于 targets 里的 `credential_ref`，例如 `postgres/mcp-lab-local`。
- 用户名：建议与 targets 的 `user` 一致（`mcp_ro`）。MCP 登录用的是 targets 的 `user`，凭据里的用户名主要用于你自己辨认。
- 密码：`mcp_ro` 的数据库口令。

写入（不回显）：

```powershell
cd postgres-mcp-server
.\scripts\set-credential.ps1 -Target mcp-lab-local -User mcp_ro
```

默认 `credential_ref` 为 `postgres/<Target>`。若 targets 里写成别的字符串，必须加 `-CredentialRef` 与之一致。

手动等价：

```powershell
cmdkey /generic:postgres/mcp-lab-local /user:mcp_ro /pass:你的口令
```

查看是否存在（看不到口令明文）：`cmdkey /list | findstr postgres`

找不到对应 Generic 项时，MCP **不会报错就停**，而是继续读 pgpass。

非 Windows 上 `lookupOS` 恒为空，只走 pgpass。

### 3.2 pgpass（Windows / Linux / macOS 通用回退）

格式与 [PostgreSQL 官方 pgpass](https://www.postgresql.org/docs/current/libpq-pgpass.html) 相同，一行五个字段，冒号分隔：

```text
hostname:port:database:username:password
```

| 平台 | 默认路径 | 覆盖 |
| --- | --- | --- |
| Windows | `%APPDATA%\postgresql\pgpass.conf` 即 `C:\Users\<用户>\AppData\Roaming\postgresql\pgpass.conf` | 环境变量 `PGPASSFILE` |
| Linux / macOS | `~/.pgpass` | 同上 |

Windows 需先建目录 `postgresql`（若没有）。Unix 上文件权限必须是 `0600` 或更严（组/其它不可读），否则 MCP **忽略该文件并报错**，避免把口令暴露在家目录。Windows 不检查 mode。

匹配规则（与本实现一致，也接近 libpq）：

- 前四段都可以写 `*` 表示通配。
- **库名、用户名大小写敏感**（`Shop` 匹配不到 `shop`）。
- 密码字段**不会**去掉首尾空格；空格是口令的一部分。
- `#` 开头（允许行首空白）为注释。
- 口令里的冒号写成 `\:`，反斜杠写成 `\\`。

实验室一行示例（口令仅用于一次性 Docker，见 `lab/postgres`）：

```text
127.0.0.1:5432:mcp_lab:mcp_ro:mcp-lab-readonly
```

同一文件里**先写出的一行优先**。不要把生产口令写进仓库里的 example。

## 4. 数据库侧：用 postgres 创建 `mcp_ro`

完整可复制 SQL（含 PG13 写法、扩展、核对）见服务 README：[用 postgres 超级用户创建 `mcp_ro`](../README.md#用-postgres-超级用户创建-mcp_ro)。

## 5. 常见失败

| 现象 | 处理 |
| --- | --- |
| `must not store password` | targets 里删掉 `password` |
| `no password for target` | Windows：`credential_ref` 与 cmdkey 目标名不一致，或没写入；再检查 pgpass 四段是否与 host/port/dbname/user 完全一致 |
| `password authentication failed` | 口令错、用户不存在、pgpass 匹配到了另一行 |
| `sslmode ... is not allowed` | 生产 target 用了 `prefer`/`disable`，改成 `require` 或 `verify-full` |
| Unix `pgpass file ... must be mode 0600` | `chmod 600 ~/.pgpass` |
| 本机 Docker 用了默认 `verify-full` | 实验室写 `"sslmode": "disable"` 且 `environment`: `dev` |
| 改了 targets 仍看到旧库 | JSON 无效时会保留上一份成功清单，看工具返回的 reload 错误 |

改完 `mcp.json` 或重新 `go build` 后，在所用客户端里重载 postgres MCP。只改 targets / pgpass / 凭据管理器，一般不用重载。
