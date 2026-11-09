package cli

import (
	"fmt"
	"strings"
)

type action string

const (
	actionHelp         action = "help"
	actionVersion      action = "version"
	actionLaunch       action = "launch"
	actionProfileList  action = "profile-list"
	actionProfileShow  action = "profile-show"
	actionProfileCheck action = "profile-check"
	actionDoctor       action = "doctor"
)

// invocation 只记录解析结果，不读取配置或启动进程。
type invocation struct {
	action         action
	id             string
	arguments      []string
	noninteractive bool
	dryRun         bool
	showArgs       bool
	jsonOutput     bool
	help           string
}

func parse(args []string) (invocation, error) {
	if len(args) == 0 {
		return invocation{action: actionHelp, help: usage}, nil
	}
	switch args[0] {
	case "--version":
		if len(args) != 1 {
			return invocation{}, fmt.Errorf("usage: hycli --version")
		}
		return invocation{action: actionVersion}, nil
	case "exec":
		return parseLaunch(args[1:], true)
	case "profile":
		return parseProfiles(args[1:])
	case "doctor":
		return parseDoctor(args[1:])
	case "help":
		return invocation{}, fmt.Errorf("help is no longer a command; use hycli --help")
	case "preview":
		return invocation{}, fmt.Errorf("preview is no longer a command; use hycli --dry-run <id> or hycli exec --dry-run <id>")
	default:
		return parseLaunch(args, false)
	}
}

func parseLaunch(args []string, noninteractive bool) (invocation, error) {
	call := invocation{action: actionLaunch, noninteractive: noninteractive}
	command, help := "hycli", usage
	if noninteractive {
		command, help = "hycli exec", execUsage
	}
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "--help":
			return invocation{action: actionHelp, help: help}, nil
		case "--dry-run":
			call.dryRun = true
		case "--show-args":
			call.showArgs = true
		default:
			return invocation{}, fmt.Errorf("unknown option %q; see %s --help", args[0], command)
		}
		args = args[1:]
	}
	if call.showArgs && !call.dryRun {
		return invocation{}, fmt.Errorf("--show-args requires --dry-run; see %s --help", command)
	}
	if len(args) == 0 {
		return invocation{}, fmt.Errorf("profile ID required; see %s --help", command)
	}
	call.id, call.arguments = args[0], args[1:]
	// 到达 profile ID 后立即停止解析，目标工具的同名选项也必须原样传递。
	if len(call.arguments) > 0 && call.arguments[0] == "--" {
		call.arguments = call.arguments[1:]
	}
	return call, nil
}

func parseProfiles(args []string) (invocation, error) {
	if len(args) == 0 || args[0] == "--help" {
		return invocation{action: actionHelp, help: profileUsage}, nil
	}
	call := invocation{}
	var help string
	switch args[0] {
	case "list":
		call.action, help = actionProfileList, profileListUsage
	case "show":
		call.action, help = actionProfileShow, profileShowUsage
	case "check":
		call.action, help = actionProfileCheck, profileCheckUsage
	default:
		return invocation{}, fmt.Errorf("unknown profile command %q; see hycli profile --help", args[0])
	}
	var ids []string
	for _, arg := range args[1:] {
		if arg == "" {
			return invocation{}, fmt.Errorf("profile ID cannot be empty; see hycli profile %s --help", args[0])
		}
		if arg == "--help" {
			return invocation{action: actionHelp, help: help}, nil
		}
		if strings.HasPrefix(arg, "-") {
			return invocation{}, fmt.Errorf("unknown option %q; see hycli profile %s --help", arg, args[0])
		}
		ids = append(ids, arg)
	}
	if len(ids) > 1 || (call.action == actionProfileList && len(ids) != 0) || (call.action == actionProfileShow && len(ids) != 1) {
		return invocation{}, fmt.Errorf("invalid arguments; see hycli profile %s --help", args[0])
	}
	if len(ids) == 1 {
		call.id = ids[0]
	}
	return call, nil
}

func parseDoctor(args []string) (invocation, error) {
	call := invocation{action: actionDoctor}
	var ids []string
	for _, arg := range args {
		switch arg {
		case "--help":
			return invocation{action: actionHelp, help: doctorUsage}, nil
		case "--json":
			call.jsonOutput = true
		default:
			if arg == "" {
				return invocation{}, fmt.Errorf("profile ID cannot be empty; see hycli doctor --help")
			}
			if strings.HasPrefix(arg, "-") {
				return invocation{}, fmt.Errorf("unknown option %q; see hycli doctor --help", arg)
			}
			ids = append(ids, arg)
		}
	}
	if len(ids) > 1 {
		return invocation{}, fmt.Errorf("expected at most one profile ID; see hycli doctor --help")
	}
	if len(ids) == 1 {
		call.id = ids[0]
	}
	return call, nil
}
