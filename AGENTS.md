# Hycli

本仓是本地 AI 工具配置启动器与外部 Agent 会话控制器。
普通启动负责 argv、env、cwd 和进程替换；session 模式每个会话使用一个 Go worker，
通过私有 Unix socket 控制 Pi RPC 或 Codex App Server，保存生命周期和原生会话映射。
不实现模型调用、Agent 循环或工具执行，不复制原生对话历史，不引入全局 daemon、HTTP 或任务数据库。
hycode 与 codex、claude、pi 一样，只是配置中的外部可执行文件，无源码依赖。
用户配置位于 HYCLI_HOME/configs，默认 ~/.hycli/configs；不读取或修改 ~/.sn。
Go 标准库优先，普通 argv 不经 shell 展开，环境变量引用在启动时解析。
session.args 独立于 args/interactiveArgs/execArgs；adapter 补充协议启动参数。
同一会话只执行一个任务；必须等待原生终态事件，不能把接受请求当作任务完成。
审批和提问必须交回控制端；未回应只取消，不自动批准；进程失联不自动重放任务。
修改后运行 gofmt、go test ./...、go vet ./...，安装测试使用临时目录。
不修改 sn-runtime，不自动提交或推送。
