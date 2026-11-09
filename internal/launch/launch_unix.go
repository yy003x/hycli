//go:build darwin || linux

// Package launch 把终端和进程身份交给目标工具，不建立第二套任务生命周期。
package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yy003x/hycli/internal/profile"
)

func Prepare(p profile.Profile) (string, string, []string, error) {
	env, err := profile.Environment(p.Env, os.Environ())
	if err != nil {
		return "", "", nil, err
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	cwd, err := profile.Expand(p.Cwd, values)
	if err != nil {
		return "", "", nil, err
	}
	if cwd == "" {
		cwd, err = os.Getwd()
	} else {
		cwd, err = filepath.Abs(cwd)
	}
	if err != nil {
		return "", "", nil, err
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", "", nil, err
	}
	if !info.IsDir() {
		return "", "", nil, fmt.Errorf("cwd is not a directory: %s", cwd)
	}
	// 由目标进程看到的 PATH 和 cwd 定位命令，避免环境切换后启动了另一个版本。
	candidates := []string{p.Command}
	if !strings.ContainsRune(p.Command, '/') {
		candidates = nil
		for _, dir := range filepath.SplitList(values["PATH"]) {
			candidates = append(candidates, filepath.Join(dir, p.Command))
		}
	}
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cwd, candidate)
		}
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, cwd, env, nil
		}
	}
	return "", "", nil, fmt.Errorf("executable %q not found or not executable in configured PATH/cwd", p.Command)
}

func Run(p profile.Profile, noninteractive bool, extra []string) error {
	args, err := p.Argv(noninteractive, extra)
	if err != nil {
		return err
	}
	command, cwd, env, err := Prepare(p)
	if err != nil {
		return err
	}
	if err := os.Chdir(cwd); err != nil {
		return err
	}
	// exec 保留 stdin/stdout/stderr、TTY、信号和退出码；hycli 不成为常驻父进程。
	return syscall.Exec(command, append([]string{p.Command}, args...), env)
}
