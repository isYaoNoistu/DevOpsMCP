# DevOpsMCP 打包

这里只负责打包，不安装、启动、挂载或注册服务，不读取连接凭据。
需要 Python 3、Go 1.26+；没有 Go 时自动使用 Docker 的 golang:1.26-bookworm 镜像编译。
首次构建需要下载依赖或镜像。

在 deploy 目录执行，不传任何参数：

- Linux：`bash pack-linux.sh`
- Windows：`pack-windows.cmd` 或 `./pack-windows.ps1`
- Linux 上打 Windows 包：`bash pack-windows.sh`

脚本自动创建 deploy/dist，固定打包 amd64，生成：

- `dist/devopsmcp-linux-amd64.tar.gz`
- `dist/devopsmcp-windows-amd64.zip`

包内包含夜莺、Jenkins、PostgreSQL、MySQL、主机日志、Kafka 六个二进制，以及公开配置示例、使用文档、许可证和 SHA256SUMS。
解压后自行在 MCP 客户端配置二进制路径和私有连接配置；示例路径和凭据需要替换。

每次使用新的临时目录构建，全部成功后替换对应压缩包；失败保留上次成功的包。临时目录自动清理，不打包旧输出目录里的私有文件。dist 不进入 Git。
旧挂载、自动注册脚本和注册模板已移除。打包不会修改客户端配置。
