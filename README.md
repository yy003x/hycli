# Hycli

本地 AI 工具配置启动器与外部 Agent 会话控制器。根据 JSON 配置启动 codex、claude、pi、hycode 或其他命令；`session` 首批支持长期控制 Pi RPC 和 Codex App Server。模型调用、上下文和工具执行由原生 Agent 负责。hycode 是独立产品，hycli 只通过进程入口使用它。

## 构建与使用

macOS/Linux，Go >= 1.25：

```sh
make build
HYCLI_HOME="$PWD" ./build/hycli profile list
HYCLI_HOME="$PWD" ./build/hycli profile check
HYCLI_HOME="$PWD" ./build/hycli hycode
HYCLI_HOME="$PWD" ./build/hycli exec hycode -- "解释当前项目"
```

仓内 configs 是可用示例，目标 AI 工具须独立安装。默认配置位置为 `~/.hycli/configs/<id>.json`，可用 HYCLI_HOME 指定；不会读取或修改 `~/.sn`。

```sh
make install                     # 安装到 ~/.local/bin/hycli
# make install PREFIX=/some/prefix
mkdir -p ~/.hycli/configs
cp -n configs/*.json ~/.hycli/configs/
hycli doctor hycode
hycli doctor --json hycode
hycli exec --dry-run hycode -- "解释当前项目"
hycli hycode
hycli exec hycode -- "解释当前项目"
git diff | hycli exec claude -- "审核这些修改"
```

## 命令结构

```text
hycli [--dry-run [--show-args]] <id> [--] [工具参数...]
hycli exec [--dry-run [--show-args]] <id> [--] [工具参数或任务...]
hycli profile list
hycli profile show <id>
hycli profile check [id]
hycli doctor [--json] [id]
hycli session <command>
hycli --help
hycli --version
```

`<id>` 是配置文件名去掉 `.json` 后的 ID，不是内置的工具名称或文件路径。
默认入口交互启动，`exec` 非交互执行；两者的 `--dry-run` 都只输出 JSON 启动预览。
`--show-args` 必须与 `--dry-run` 一起使用。启动选项必须放在 ID 前，ID 后的所有参数都属于目标工具；紧跟 ID 的第一个可选 `--` 会被移除。

```sh
hycli --dry-run hycode
hycli exec --dry-run --show-args hycode -- "解释当前项目"
hycli exec --help              # hycli 的 exec 帮助，不读取配置
hycli exec hycode --help       # 将 --help 交给 hycode
hycli profile --help
hycli profile show --help
hycli doctor --help
```

帮助统一使用各级 `--help`，版本只使用顶层 `--version`，不提供短选项。
无参数的 `hycli` 和 `hycli profile` 显示对应帮助；帮助和版本写入 stdout 并成功退出。
非法命令或选项写入 stderr 并非零退出。`help` 和 `preview` 已移除，不保留别名；旧调用会报错并提示新入口。

`profile` 管配置，`--dry-run` 预演直接启动，`doctor` 检查本机启动条件，`session` 管原生会话与进程生命周期。`session open` 的参数仍然是 profile ID，可以配置多个 Pi/Codex 环境。

## 会话控制

仓内 `pi`、`codex` 示例已启用 session。已有个人配置需要增加 `session` 字段，安装器不会覆盖配置：

```json
{
  "command": "codex",
  "execArgs": ["exec"],
  "session": {"adapter": "codex", "args": []}
}
```

Pi 对应 `"command":"pi"`、`"execArgs":["--print"]`、`"adapter":"pi"`。
`session.args` 是独立参数列表，不拼接普通启动的 `args`、`interactiveArgs` 或 `execArgs`；仍继承 profile 的 command、env、cwd。协议模式参数由 adapter 添加。

```sh
hycli session open --cwd "$PWD" codex
# 从 JSON 的 session.id 取出 s_...；以下使用该 ID
hycli session send --wait s_... "阅读项目，说明主要模块"
hycli session send --wait s_... "基于刚才的上下文继续分析"
hycli session status s_...
hycli session attach s_...
hycli session interrupt s_...
hycli session close s_...
hycli session resume s_...
hycli session list
```

每个会话拥有一个 Go worker 和一个 Agent 进程，后续 send 复用原生会话。`send` 默认返回 worker 的接收结果；`--wait` 输出 JSONL 事件直到任务终态。收到 `input.required` 后，在另一终端用 `session respond` 回答；不会自动批准。`attach` 观察结构化事件，Ctrl-C 只断开观察。详细命令、审批示例、状态与恢复边界见 [session 使用与契约](docs/session.md)。

## 配置契约

```json
{
  "command": "hycode",
  "args": [],
  "interactiveArgs": [],
  "execArgs": ["--print"],
  "env": {"HYCODE_CODING_AGENT_DIR": "${HOME}/.hycode/work"},
  "cwd": "${HOME}/work"
}
```

- `command` 必填，可为 PATH 命令或路径；相对路径基于目标 cwd。不限工具名称。
- profile ID 不能使用 `exec`、`profile`、`doctor`、`session`，以及已移除入口 `preview`、`help` 这些保留名称；加载时直接拒绝，避免旧调用意外启动同名工具。
- `args` 是两种模式共用参数；交互追加 `interactiveArgs`，exec 追加 `execArgs`，最后追加命令行中的参数。每个字符串原样对应一个 argv token，不拼接 shell 命令。
- 缺少 `execArgs` 时拒绝 exec；空数组表示工具无需额外的非交互参数。提示词如何表达由目标工具决定。
- `env` 覆盖继承环境；`${VAR}` 从启动时继承环境解析，缺失时报错，不依赖同一 env 对象中其他条目的解析顺序。`cwd` 的变量使用覆盖后的环境解析，缺省继承当前目录。
- `args` 不展开变量，`command` 不展开 `~` 或变量。凭据通过环境变量引用传递，不写到参数或 JSON 明文。
- `profile list/show/check` 只读；check 检查配置，doctor 进一步检查变量、目录及可执行文件，不启动工具。show 输出原始声明，不展开变量。
- `doctor [--json] [id]` 说明配置来源、实际可执行文件、cwd、exec 支持情况和缺失变量；JSON 带 schemaVersion 与每个 profile 的就绪结果，失败返回非零退出码。
- `--dry-run` 输出启动预览而不执行。默认隐藏所有参数值，环境只显示变量引用或隐藏标记；显式 `--show-args` 才展示完整 argv，因此不要用于含凭据的参数。`profile show` 展示配置声明，`--dry-run` 展示本次启动解析结果；声明检查不替代目标工具自身的配置/认证检查。
- 普通交互与 exec 启动用进程替换，保留 TTY、stdin/stdout/stderr、信号和退出码，不创建 worker。Ctrl-C 和终端尺寸由目标工具原生处理。只有显式 `session open/resume` 创建后台 worker。

API Profile 未迁入新仓，旧能力仍保留在未修改的 sn-runtime。hycli 与 hycode 独立安装发布，不共享配置或底座。session 不引入全局 daemon、HTTP 服务、任务数据库或第二套对话历史；本期尚未实现 Claude、hycode 的 session adapter。

验证：`make check`；默认测试不调用 Provider。真实 Pi/Codex 初始化、Pi 扩展问答及显式模型测试入口见 [session 验证](docs/session.md#验证)。

安装器使用同目录临时文件原子替换，只更新可验证为本产品的 Go 二进制；拒绝覆盖其他命令和软链接，保留全部用户配置。`make build && sh scripts/check-install.sh` 在临时目录验证首次安装、更新和冲突保护。

`make release VERSION=0.2.0` 生成 macOS/Linux × arm64/amd64 归档及 SHA-256 `checksums.txt`，输出到 `release/0.2.0/`。`VERSION=0.2.0 sh scripts/check-release.sh` 检查归档和本机可执行文件。下载归档先验证校验和，再安装其中的二进制。推送 `v*` 标签后工作流创建 GitHub draft release；本地构建不会提交、推送或发布。交叉构建不等于各平台真实执行验收。

可选的双产品集成验证（仅依赖构建后的命令，不读取 hycode 源码）：

```sh
python3 scripts/check-hycode.py ./build/hycli /path/to/hycode/bin/hycode
```
