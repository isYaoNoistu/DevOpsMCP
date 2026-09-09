# DevOpsMCP deploy

本目录是**默认发布入口**：Linux / Windows 打包脚本，以及接到月弦时的挂载与注册。

| 你要干什么 | 跑什么 |
| --- | --- |
| 本机 Cursor / WorkBuddy 用（Windows） | `pack-windows.cmd` |
| 本机或服务器用（Linux） | `./pack-linux.sh` |
| 在 Linux 上交叉编出 Windows zip | `./pack-windows.sh` |
| 月弦 Docker 已启动，把 MCP 挂进去并注册 | 先起月弦，再 `./attach.sh` |

编译三个二进制共用 `compile.py`（有 Go 本机编，没有就用 `golang:1.23-bookworm`）。`deploy/dist/` 不进 git。

仓库和 `.env` 里不要写真实 Token、生产密码、内网主机名。

---

## 1. Linux 打包

```bash
cd deploy
chmod +x pack-linux.sh pack-windows.sh attach.sh
./pack-linux.sh
```

产出：

- `dist/devopsmcp-linux-amd64/` — 三个 ELF + `examples/` + `PACK.txt`
- `dist/devopsmcp-linux-amd64.tar.gz`

```bash
./pack-linux.sh --arch arm64
./pack-linux.sh --outdir /tmp/mcp --no-archive
./pack-linux.sh --docker          # 强制走镜像
```

国内可设 `GOPROXY=https://goproxy.cn,direct`。无密钥样例来自仓库 `examples/`，解压后把 `command` 改成这个目录的绝对路径。

---

## 2. Windows 打包

本机（PowerShell / cmd）：

```bat
cd deploy
pack-windows.cmd
```

或：

```powershell
.\pack-windows.ps1
.\pack-windows.ps1 -Arch arm64
.\pack-windows.ps1 -Docker
```

产出 `dist/devopsmcp-windows-amd64/`（三个 `.exe`）和 `.zip`。

在 Linux / CI 上交叉编译同样一份 zip：

```bash
./pack-windows.sh
```

不要把 Windows `.exe` 挂进月弦 Linux 容器。

---

## 3. 接到月弦（YLune）

月弦怎么启动仍看它自己的 `deploy/`：`docker compose up -d --build`。容器固定挂 `/opt/mcp`（宿主机默认 `/data/ylune-mcp`）。

本脚本：往挂载点写 **Linux** 二进制、可选生成 Postgres 清单/pgpass、按 `.env` 调月弦 API 注册。

```text
/data/YLuneMCPHub     # 月弦，先启动
/data/DevOpsMCP       # 本仓库
/data/ylune-mcp       # 挂载点，不进 git
```

```bash
cd /data/YLuneMCPHub/deploy
cp .env.example .env    # ADMIN_PASSWORD、DB_PASSWORD
docker compose up -d --build

cd /data/DevOpsMCP/deploy
cp .env.example .env    # Token；REGISTER_* ；改过管理员密码则写 YLUNE_PASSWORD
./attach.sh
```

脚本会找 `/data/YLuneMCPHub` 和容器 `ylune` 的 `/opt/mcp`。然后在月弦控制台确认已连接，再**分组、加成员**。管理员不必入组。

### `.env` 注册开关

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `REGISTER_NIGHTINGALE` | `true` | 注册 `nightingale`（还要 `N9E_TOKEN`） |
| `REGISTER_JENKINS` | `true` | 注册 `jenkins`（还要 `JENKINS_API_TOKEN`） |
| `REGISTER_POSTGRES` | `false` | 写 targets + pgpass，并注册 `postgres` |
| `YLUNE_HOME` / `YLUNE_URL` / `MCP_MOUNT_DIR` | 自动找 | 对不上再手填 |
| `YLUNE_PASSWORD` | 可回落首次 `ADMIN_PASSWORD` | 控制台当前密码 |

同机夜莺 / Jenkins / 库用 `host.docker.internal`，不要 `127.0.0.1`。已有 pg 文件默认不覆盖：`OVERWRITE_PG_CONFIG=true` 才重写。

```bash
./attach.sh
./attach.sh --build-only
./attach.sh --register-only
./attach.sh --discover
```

---

## 4. 本目录文件

| 文件 | 说明 |
| --- | --- |
| `compile.py` | 真正执行 `go build` / docker 编译 |
| `pack-linux.sh` | Linux 默认打包 |
| `pack-windows.ps1` / `.cmd` | Windows 默认打包 |
| `pack-windows.sh` | 在 Linux 上打 Windows 包 |
| `attach.sh` | 写进月弦挂载点并注册 |
| `register.py` | 找月弦、写 pg 文件、调 API |
| `.env.example` | attach 用，复制为 `.env`，勿提交 |
| `templates/postgres-targets.json` | 文档用样例（attach 按 `.env` 生成） |

---

## 5. 常见失败

| 现象 | 处理 |
| --- | --- |
| `need Go 1.23+ or Docker` | 安装 Go，或 Docker 能拉 `golang:1.23-bookworm` |
| 包里是 PE32 / `.exe` 却挂进 Linux 容器 | 用 `pack-linux.sh` 或 `attach.sh`，不要用 Windows 包 |
| `missing deploy/.env` | 仅 attach 需要；打包脚本不需要 `.env` |
| 容器没有 `/opt/mcp` | 更新月弦仓后 `docker compose up -d` |
| 登录月弦失败 | 控制台已改密，写 `YLUNE_PASSWORD` |
| 普通用户 `/mcp` 没工具 | 还没进组 |

本机不经过月弦、直接 stdio 的字段说明仍看仓库根 README。
