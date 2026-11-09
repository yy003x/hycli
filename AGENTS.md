# Hycli

本仓是本地 AI 工具配置启动器。只负责 argv、env、cwd、进程替换和配置管理。
不实现模型调用、Agent 循环、工具执行、会话持久化、数据库或服务端。
hycode 与 codex、claude、pi 一样，只是配置中的外部可执行文件，无源码依赖。
用户配置位于 HYCLI_HOME/configs，默认 ~/.hycli/configs；不读取或修改 ~/.sn。
Go 标准库优先，普通 argv 不经 shell 展开，环境变量引用在启动时解析。
修改后运行 gofmt、go test ./...、go vet ./...，安装测试使用临时目录。
不修改 sn-runtime，不自动提交或推送。
