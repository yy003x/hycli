# Hycli

本地 AI 工具配置启动器。根据 JSON 配置启动 codex、claude、pi、hycode 或其他可执行命令。不实现模型调用、Agent 循环、工具系统、会话数据库或服务端。hycode 是独立产品，hycli 只通过进程入口使用它。

## 构建与使用

macOS/Linux，Go >= 1.25：

```sh
make build
HYCLI_HOME="$PWD" ./dist/hycli profile list
HYCLI_HOME="$PWD" ./dist/hycli profile check
HYCLI_HOME="$PWD" ./dist/hycli hycode
HYCLI_HOME="$PWD" ./dist/hycli exec hycode -- "解释当前项目"
```

仓内 configs 是可用示例，目标 AI 工具须独立安装。默认配置位置为 `~/.hycli/configs/<id>.json`，可用 HYCLI_HOME 指定；不会读取或修改 `~/.sn`。

```sh
make install                     # 安装到 ~/.local/bin/hycli
# make install PREFIX=/some/prefix
mkdir -p ~/.hycli/configs
cp -n configs/*.json ~/.hycli/configs/
hycli doctor hycode
hycli hycode
hycli exec hycode -- "解释当前项目"
git diff | hycli exec claude -- "审核这些修改"
```

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
- `args` 是两种模式共用参数；交互追加 `interactiveArgs`，exec 追加 `execArgs`，最后追加命令行中的参数。每个字符串原样对应一个 argv token，不拼接 shell 命令。
- 缺少 `execArgs` 时拒绝 exec；空数组表示工具无需额外的非交互参数。提示词如何表达由目标工具决定。
- `env` 覆盖继承环境；`${VAR}` 从启动时继承环境解析，缺失时报错，不依赖同一 env 对象中其他条目的解析顺序。`cwd` 的变量使用覆盖后的环境解析，缺省继承当前目录。
- `args` 不展开变量，`command` 不展开 `~` 或变量。凭据通过环境变量引用传递，不写到参数或 JSON 明文。
- `profile list/show/check` 只读；check 检查配置，doctor 进一步检查变量、目录及可执行文件，不启动工具。show 输出原始声明，不展开变量。
- 启动后用 exec 替换当前进程，保留 TTY、stdin/stdout/stderr、信号和退出码；没有后台 worker。Ctrl-C 和终端尺寸由目标工具原生处理。

API Profile 未迁入新仓，旧能力仍保留在未修改的 sn-runtime。hycli 与 hycode 独立安装发布，不共享配置或底座。未来的无 TUI 协议集成另行设计，本期只使用 CLI。

验证：`make check`；真实 CLI 集成测试使用临时配置及系统命令，不调用 Provider。

可选的双产品集成验证（仅依赖构建后的命令，不读取 hycode 源码）：

```sh
python3 scripts/check-hycode.py ./dist/hycli /path/to/hycode/bin/hycode
```
