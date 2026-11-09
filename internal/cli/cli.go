package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yy003x/hycli/internal/buildinfo"
	"github.com/yy003x/hycli/internal/launch"
	"github.com/yy003x/hycli/internal/profile"
)

func Run(args []string, stdout io.Writer) error {
	call, err := parse(args)
	if err != nil {
		return err
	}
	// 帮助与版本不依赖配置和目标程序，配置损坏时仍能查看用法。
	switch call.action {
	case actionHelp:
		_, err := io.WriteString(stdout, call.help)
		return err
	case actionVersion:
		_, err := fmt.Fprintln(stdout, "hycli", buildinfo.Version)
		return err
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
	switch call.action {
	case actionLaunch:
		if call.dryRun {
			return dryRun(dir, call, stdout)
		}
		p, err := profile.Load(dir, call.id)
		if err != nil {
			return err
		}
		return launch.Run(p, call.noninteractive, call.arguments)
	case actionProfileList:
		return listProfiles(dir, stdout)
	case actionProfileShow:
		return showProfile(dir, call.id, stdout)
	case actionProfileCheck:
		return checkProfiles(dir, call.id, stdout)
	case actionDoctor:
		return doctor(dir, call.id, call.jsonOutput, stdout)
	default:
		return fmt.Errorf("unsupported command action %q", call.action)
	}
}
