package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpAndVersionDoNotReadConfiguration(t *testing.T) {
	// 用普通文件代替配置根目录，确保帮助不会访问或初始化配置目录。
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, "Usage:"},
		{[]string{"--help"}, "Usage:"},
		{[]string{"--version"}, "hycli "},
		{[]string{"--dry-run", "--help"}, "Usage:"},
		{[]string{"exec", "--help"}, "Usage: hycli exec"},
		{[]string{"exec", "--dry-run", "--help"}, "Usage: hycli exec"},
		{[]string{"profile"}, "Usage: hycli profile"},
		{[]string{"profile", "--help"}, "Usage: hycli profile"},
		{[]string{"profile", "list", "--help"}, "Usage: hycli profile list"},
		{[]string{"profile", "show", "--help"}, "Usage: hycli profile show"},
		{[]string{"profile", "show", "tool", "--help"}, "Usage: hycli profile show"},
		{[]string{"profile", "check", "--help"}, "Usage: hycli profile check"},
		{[]string{"doctor", "--help"}, "Usage: hycli doctor"},
		{[]string{"doctor", "--json", "tool", "--help"}, "Usage: hycli doctor"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			cmd := command(home, test.args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil || !strings.Contains(string(out), test.want) || stderr.Len() != 0 {
				t.Fatalf("%v: stdout=%s stderr=%s", err, out, &stderr)
			}
		})
	}
}

func TestInvalidCommandsAndOptionsDoNotLaunch(t *testing.T) {
	work := t.TempDir()
	marker := filepath.Join(work, "launched")
	home := setup(t, map[string]any{"command": "/usr/bin/touch", "args": []string{marker}, "execArgs": []string{}, "cwd": work})
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"help"}, "use hycli --help"},
		{[]string{"preview", "tool"}, "use hycli --dry-run"},
		{[]string{"profile", "preview", "tool"}, "unknown profile command"},
		{[]string{"-h"}, "unknown option"},
		{[]string{"--verison"}, "unknown option"},
		{[]string{"--version", "tool"}, "usage: hycli --version"},
		{[]string{"exec", "--version"}, "unknown option"},
		{[]string{"doctor", "--unknown", "tool"}, "unknown option"},
		{[]string{"profile", "list", "--json"}, "unknown option"},
		{[]string{"profile", "show"}, "invalid arguments"},
		{[]string{"profile", "list", "tool"}, "invalid arguments"},
		{[]string{"profile", "check", ""}, "profile ID cannot be empty"},
		{[]string{"doctor", ""}, "profile ID cannot be empty"},
		{[]string{"--show-args", "tool"}, "requires --dry-run"},
		{[]string{"exec", "--show-args", "tool"}, "requires --dry-run"},
		{[]string{"--dry-run"}, "profile ID required"},
		{[]string{"exec"}, "profile ID required"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			cmd := command(home, test.args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err == nil || len(out) != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("%v: stdout=%s stderr=%s", err, out, &stderr)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("invalid command launched the target: %v", err)
			}
		})
	}
}
