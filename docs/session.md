# Session 使用与契约

`hycli session` 控制外部 Agent 的长期进程与原生会话。本期 adapter 为 `pi` 和 `codex`，控制器全部使用 Go；任务、模型、工具、Skills、MCP 和历史存储仍由各 Agent 实现。普通 `hycli <profile>` 与 `hycli exec <profile>` 的行为不变。

## 配置与职责

```json
{
  "command": "pi",
  "execArgs": ["--print"],
  "session": {"adapter": "pi", "args": []}
}
```

Codex 使用 `command: "codex"`、`execArgs: ["exec"]`、`session.adapter: "codex"`。
profile ID 只是文件名，因此 `pi-work`、`codex-personal` 等配置都可独立使用。

- `command`、`env`、`cwd` 沿用 profile；`open --cwd` 覆盖本次工作目录。
- `session.args` 缺省为空，是协议进程的完整额外 argv；不继承普通启动三组参数，也不展开 shell 或环境变量。Pi 可在其中设置 `--model`、`--session-dir` 等；Codex 可使用 `-c` 配置参数。
- adapter 分别追加 `--mode rpc` 或 `app-server --listen stdio://`。不得自行传入 mode、app-server、listen、resume 等由控制器管理的参数，也不要在这里附带任务提示词。
- 模型、认证、权限和扩展读取原生 Agent 的有效配置。hycli 不提供 LLM API 配置或凭据副本，不强制放宽 Codex 沙箱/审批策略。
- `profile check` 检查配置结构，`doctor` 检查通用启动条件；原生参数和协议初始化是否可用由 `session open` 验证。

每个会话对应一个 worker、一个 Agent 进程、一个当前原生会话。控制客户端通过当前用户私有 Unix socket 访问 worker；关闭命令行终端不会主动关闭会话。没有全局 daemon、HTTP 服务、任务队列或 SQLite 数据库。

## 命令

| 命令 | 行为 |
| --- | --- |
| `session open [--cwd <dir>] <profile>` | 新建 worker 和原生会话，完成协议初始化后返回 |
| `session list` | 列出本机控制记录，包含已停止会话 |
| `session status <id>` | 查询进程、原生 ID、当前任务和待回答问题 |
| `session send [--wait] <id> [--] <prompt>` | 给原生会话发送一个文本参数；忙时拒绝，不排队 |
| `session attach [--after <sequence>] <id>` | 读取有限事件回放并持续观察输出 |
| `session respond <id> <request-id> <json>` | 回答指定审批或问题 |
| `session interrupt <id>` | 请求中断当前任务，保留进程 |
| `session close <id>` | 关闭 Agent 与 worker，保留原生历史和控制记录 |
| `session resume <id>` | 以新进程恢复同一原生会话，使用当前 profile 与原工作目录 |

各级帮助均为 `--help`。选项必须在 profile/session ID 之前。提示词含空格时作为一个带引号参数传入；首个 `--` 是可选分隔符，不是 Agent 原始参数透传。本期不支持 stdin 提示词、图片、steer、原生 TUI attach 或外部既有会话导入。

```sh
hycli session open --cwd "$PWD" pi
# 将返回的 session.id 赋给会话变量
session_id=s_...
hycli session send --wait "$session_id" '记住：本次目标是审查命令解析'
hycli session send --wait "$session_id" '根据刚才的目标继续'
hycli session close "$session_id"
hycli session resume "$session_id"
hycli session send --wait "$session_id" '我们刚才的目标是什么？'
```

## 接收、执行和终态

普通命令返回 JSON：`{"ok":true,"session":{...}}`；list 返回 `{"ok":true,"sessions":[...]}`。`send --wait` 与 `attach` 输出 JSONL，每行是 session 快照或 `event`。普通命令失败写 stderr 并非零退出；流式响应可能先输出事件，再非零退出。原生初始化最多等待 60 秒，prompt 确认最多等待五分三十秒，普通原生控制调用最多等待 30 秒；已确认任务的模型执行没有统一时长上限。

会话状态：`starting`、`idle`、`submitted`、`running`、`waiting_input`、`interrupting`、`closing`、`closed`、`failed`。无法验证 worker 可达时，查询显示 `unreachable`，不据旧 PID 猜测进程仍然存活。

任务状态与会话状态分开：`submitted → running → completed/failed/interrupted/handled/unknown`。`handled` 表示 Pi 扩展命令已处理，没有正在执行的 Agent 任务；若扩展启动后续原生任务，会另外跟踪其事件。`unknown` 表示未取得可靠终态，不应自动重新提交。

- `send` 默认在 worker 接收并登记任务后返回；此时 `turn.accepted` 可能尚未到达。
- `turn.accepted` 才表示原生 Agent 已确认请求。Pi 扩展允许先提问、后确认，因此事件流从提交时就开放。
- `turn.completed` 是本次 hycli 任务终态。Pi 等待 `agent_settled`，不把重试/压缩中间的 `agent_end` 当作终态；Codex 等待 `turn/completed`。
- `send --wait` 在 completed/handled 时成功退出，失败、中断、未知或失联时非零退出。
- 同一会话不接受并发任务，多个会话可独立执行。审批、提问期间仍然算忙。
- Ctrl-C 只结束本次观察或等待命令；明确取消任务使用 `interrupt`。Pi 中断前会清空原生待发队列，避免 abort 后继续执行排队内容。

事件包装：

```json
{"ok":true,"event":{"sequence":12,"type":"input.required","turnId":"t_...","data":{"request":{"id":"q_...","method":"confirm","expiresAt":"..."},"params":{"title":"允许执行？"}}}}
```

`agent.event` 保留原生事件结构；hycli 另发 `session.ready`、`turn.submitted`、`turn.accepted`、`turn.completed`、`input.required`、`input.resolved`、`session.closed/failed` 等控制事件。不得依赖每个 Agent 拥有相同的原生 token/tool 事件形状。

事件仅在内存中保留最近 512 条、约 32 MiB，重启后不保留；截断时发送 `events.gap`。sequence 在同一 hycli 会话中持续递增，原生历史仍由 Agent 保存。慢读或断开的观察端不阻塞 Agent；`status` 的在线 `pending[].params` 保留尚未回答的问题全文，即使相关事件已被截断。

## 审批与提问

收到 `input.required` 后，从 `data.request.id` 取 hycli 请求 ID，并按 `method` 使用 `data.params` 作决定，在另一个终端响应。断线重连也可用 `status` 查看当前 pending 请求。请求不回应时最多等待五分钟；Pi 自带更短超时时使用较短值，过期只取消/拒绝，绝不自动批准。

| 原生请求 | 响应 JSON 示例 |
| --- | --- |
| Pi `confirm` | `{"confirmed":true}` 或 `{"confirmed":false}` |
| Pi `input` / `editor` / `select` | `{"value":"回答或一个有效选项"}` |
| Pi 取消任意对话框 | `{"cancelled":true}` |
| Codex commandExecution / fileChange 审批 | `{"decision":"accept"}`；也支持 acceptForSession、decline、cancel |
| Codex permissions 审批 | `{"permissions":{},"scope":"turn"}` 拒绝新增权限；批准时依据原生请求明确给出 permissions |
| Codex requestUserInput | `{"answers":{"question-id":{"answers":["所选答案"]}}}` |
| Codex MCP elicitation | `{"action":"cancel","content":null}`；接受使用 accept 和请求 schema 对应的 content |

```sh
hycli session respond s_... q_... '{"decision":"accept"}'
hycli session respond s_... q_... '{"value":"继续审查"}'
```

已过期/已回答请求会拒绝重复回答。未知 Codex client request 显式返回协议错误，不静默批准。首版不支持修改 execpolicy/network policy 的复合审批 decision，也不提供原生自定义 TUI 组件。Pi 通知仍会输出，但不当作待回答对话框。

审批仅转发 Agent 原本发出的请求。Pi 内置工具并不普遍强制审批；是否询问由其原生行为和扩展决定。hycli 不额外提供工具沙箱或拦截每次工具调用。会改变原生会话身份的 Pi 扩展命令不属于本期支持范围，生命周期统一通过 hycli 的 open/close/resume 控制。

## 关闭、恢复与本地文件

控制记录位于 `${HYCLI_HOME:-~/.hycli}/sessions/<id>/`，目录 0700、元数据/日志 0600：

- `session.json`：profile、工作目录、进程代次、原生 ID/path、最近任务结果和待回答请求摘要；不保存提示词、问题全文、对话历史或解析后的环境变量。
- `worker.lock`：单会话排他租约，防止同时恢复两份 worker。
- `agent.stderr.log`：当前进程的原生诊断，最多 1 MiB；可能包含 Agent 自己输出的敏感信息。hycli 不承诺清洗任意第三方日志。
- `worker.log`：worker 启动/故障诊断。

socket 位于 `/tmp/hycli-<uid>/` 的私有短路径目录，避免 macOS Unix socket 长度限制。控制请求绑定 instance；resume 生成新 instance，不让旧客户端控制新进程。PID 仅用于诊断，不用于离线强制关闭未知进程。

`close` 先关闭 Agent stdin，必要时升级为进程组 TERM/KILL，并等待 worker 释放租约；重复 close 可用。这里管理原生 Agent 进程，不保证撤销已经产生的外部副作用或关闭工具自行脱离的后台进程。

异常断线/退出不会自动重新发送任务；保留失败或未知结果。worker 仍持有租约时 resume 会拒绝。原生历史存在且旧 worker 已结束后，可显式 resume：Pi 使用 session file，Codex 使用 thread ID。Pi 空会话可能尚未落盘，缺失文件时会说明无法恢复；原生历史被删除、原工作目录不存在或配置已不兼容时也会报错。

resume 恢复的是已持久化上下文，不恢复半途执行的工具，也不是 checkpoint 自动续跑。hycli 不管理原生历史的删除、压缩或保留周期，不复制它们到控制目录。

## 验证

实际验证版本：Pi 1.5.0、Codex 0.167.0。实现直接依赖这两种协议，其他版本须验证，不承诺任意旧版本兼容。Codex 协议参考[官方 App Server 文档](https://learn.chatgpt.com/docs/app-server)，Pi 使用当前安装包随附的 `docs/rpc*.md`。

```sh
make check
# 真实协议初始化和生命周期，不调用模型；临时 HOME，不读取个人认证
HYCLI_SESSION_INTEGRATION=1 HYCLI_TEST_RACE=1 go test -race ./cmd/hycli -run TestSessionReal -count=1
# 可选：指定真实 Pi 安装包的 rpc-demo 扩展，验证问答往返和取消
HYCLI_SESSION_INTEGRATION=1 HYCLI_TEST_RACE=1 \
  HYCLI_PI_RPC_DEMO=/path/to/pi-coding-agent/examples/extensions/rpc-demo.ts \
  go test -race ./cmd/hycli -run TestSessionReal -count=1

make build
# 明确调用模型，使用原生已有认证；控制记录和项目文件使用临时目录
python3 scripts/check-session-live.py ./build/hycli --live
python3 scripts/check-session-live.py ./build/hycli --live --codex-approval-only
```

live 测试验证真实多轮上下文、原生恢复、拒绝并发、中断与进程退出；审批用例在临时项目目录批准一次明确的文件写入并读取结果。测试输出 receipt 路径；Codex/Pi 原生历史仍遵循各自配置。本期真实验证包含 Pi 扩展 input/取消及 Codex 命令审批；其他审批/elicitation 变体有协议识别测试，尚未逐项通过真实工具触发。
