# Jenkins Job 路径

`job_path` 一律用斜杠。URL `https://jenkins.example.com/job/team/job/prod/job/checkout-api/22/` 写成：

```text
job_path: team/prod/checkout-api
build_number: 22
```

上面的 `team` / `checkout-api` 是虚构示例。你们环境的 Folder 以 `list_jobs` 为准。

## 怎么找

1. 用户只说服务名时，先 `list_jobs`：`folder_path` 用已知业务 Folder，`name_filter` 用关键字。
2. 不要把 Git 仓库目录路径直接当成 `job_path`。Jenkins 上常常多一层环境 Folder，例如 `team/prod/checkout-api`。
3. 发版排查优先从业务 Folder 进，不要默认对根做 `recursive: true`。

| 用户说法 | 建议 `folder_path` | `name_filter` |
|---|---|---|
| 「checkout 生产」 | 你们的生产 Folder，如 `team` | `checkout` |
| 「所有失败」 | 收窄后的 Folder | 可空，改用 `find_recent_failures` |

命中后再用返回的 `job_path` 下钻。

## 构建参数

以当次 `get_build_info` 为准。常见的只是举例：分支名、操作类型、目标主机。凭据 **ID** 可以点名，不要去读凭据内容。
