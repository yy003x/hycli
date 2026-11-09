package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yy003x/hycli/internal/launch"
	"github.com/yy003x/hycli/internal/profile"
)

var version = "0.1.0-dev"

const usage = `hycli — local AI tool launcher

Usage:
  hycli <profile> [--] [tool arguments...]
  hycli exec <profile> [--] [tool arguments/prompt...]
  hycli profile list|show <id>|check [id]
  hycli doctor [id]
  hycli --help | --version

Profiles: ${HYCLI_HOME:-~/.hycli}/configs/<id>.json
Each args element is one argv token. Arguments after the profile pass through.
exec requires execArgs in the profile. Stdin is inherited, including pipes.
No LLM API, agent loop, database, daemon, or server is embedded.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hycli:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}
	if args[0] == "--version" {
		fmt.Println("hycli", version)
		return nil
	}
	home := os.Getenv("HYCLI_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(userHome, ".hycli")
	}
	dir, err := filepath.Abs(filepath.Join(home, "configs"))
	if err != nil {
		return err
	}
	if args[0] == "profile" {
		return profiles(dir, args[1:])
	}
	if args[0] == "doctor" {
		return check(dir, args[1:], true)
	}
	noninteractive := args[0] == "exec"
	if noninteractive {
		args = args[1:]
	}
	if len(args) == 0 {
		return fmt.Errorf("profile ID required; see hycli --help")
	}
	p, err := profile.Load(dir, args[0])
	if err != nil {
		return err
	}
	extra := args[1:]
	if len(extra) > 0 && extra[0] == "--" {
		extra = extra[1:]
	}
	return launch.Run(p, noninteractive, extra)
}

func profiles(dir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected profile list|show|check")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: hycli profile list")
		}
		ids, err := profile.IDs(dir)
		if err != nil {
			return err
		}
		for _, id := range ids {
			fmt.Println(id)
		}
		return nil
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: hycli profile show <id>")
		}
		p, err := profile.Load(dir, args[1])
		if err != nil {
			return err
		}
		// 展示声明，不解析环境变量，避免把已解析凭据打印到终端。
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(p)
	case "check":
		return check(dir, args[1:], false)
	default:
		return fmt.Errorf("unknown profile operation %q", args[0])
	}
}

func check(dir string, ids []string, executable bool) error {
	if len(ids) > 1 {
		return fmt.Errorf("expected at most one profile ID")
	}
	if len(ids) == 0 {
		var err error
		ids, err = profile.IDs(dir)
		if err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("no profiles in %s", dir)
	}
	failures := []string{}
	for _, id := range ids {
		p, err := profile.Load(dir, id)
		if err == nil && executable {
			_, _, _, err = launch.Prepare(p)
		}
		if err != nil {
			failures = append(failures, id+": "+err.Error())
		} else {
			fmt.Println(id + ": ok")
		}
	}
	if len(failures) != 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}
