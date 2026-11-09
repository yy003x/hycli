package cli

const usage = `hycli — local AI tool launcher

Usage:
  hycli [--dry-run [--show-args]] <id> [--] [tool arguments...]
  hycli exec [--dry-run [--show-args]] <id> [--] [tool arguments/prompt...]
  hycli profile <command>
  hycli doctor [--json] [id]
  hycli --help
  hycli --version

Commands:
  exec       Launch a profile in noninteractive mode
  profile    List, show, or check profile configurations
  doctor     Check local launch readiness without starting a tool

Options:
  --dry-run    Print launch details as JSON without starting the tool
  --show-args  Show full arguments in --dry-run output (hidden by default)
  --help       Show help for the current command
  --version    Show the hycli version (top level only)

Use hycli <command> --help for command-specific help.
Launch options must precede the profile ID. All arguments after it pass through.
Profiles: ${HYCLI_HOME:-~/.hycli}/configs/<id>.json
No LLM API, agent loop, database, daemon, or server is embedded.
`

const execUsage = `Usage: hycli exec [--dry-run [--show-args]] <id> [--] [tool arguments/prompt...]

Launch a profile in noninteractive mode. The profile must define execArgs.
Stdin, stdout, stderr, signals, and the tool's exit status are preserved.

Options (before the profile ID):
  --dry-run    Print launch details as JSON without starting the tool
  --show-args  Show full arguments in --dry-run output (hidden by default)
  --help       Show this help without loading configuration

Each argument is one argv token; no shell expansion is performed by hycli.
All arguments after the profile ID pass through, including --help and --dry-run.
An optional first -- after the profile ID is removed.
`

const profileUsage = `Usage: hycli profile <command>

Read-only profile configuration commands:
  list         List profile IDs
  show <id>    Show the original configuration without expanding variables
  check [id]   Validate one profile, or all profiles when id is omitted

Use hycli profile <command> --help for command-specific help.
Profiles: ${HYCLI_HOME:-~/.hycli}/configs/<id>.json
To inspect a launch, use hycli --dry-run <id> or hycli exec --dry-run <id>.
`

const profileListUsage = `Usage: hycli profile list

List profile IDs in the configuration directory without launching any tool.

Options:
  --help  Show this help without loading configuration
`

const profileShowUsage = `Usage: hycli profile show <id>

Show the original profile JSON. Environment variable references are not expanded.

Options:
  --help  Show this help without loading configuration
`

const profileCheckUsage = `Usage: hycli profile check [id]

Validate one profile, or all profiles when id is omitted.
This checks configuration only; use hycli doctor to check local launch readiness.

Options:
  --help  Show this help without loading configuration
`

const doctorUsage = `Usage: hycli doctor [--json] [id]

Check one profile, or all profiles when id is omitted.
Checks configuration, environment references, working directory, and executable.
Does not launch tools or verify provider authentication. Failures exit nonzero.

Options:
  --json  Print structured readiness results (may appear before or after id)
  --help  Show this help without loading configuration
`
