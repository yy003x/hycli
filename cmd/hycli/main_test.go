package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var testBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hycli-build-")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(dir, "hycli")
	if output, err := exec.Command("go", "build", "-o", testBinary, ".").CombinedOutput(); err != nil {
		panic(string(output) + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func setup(t *testing.T, config map[string]any) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "configs"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "configs/tool.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return home
}

func command(home string, args ...string) *exec.Cmd {
	cmd := exec.Command(testBinary, args...)
	cmd.Env = []string{"HYCLI_HOME=" + home, "HOME=" + home, "PATH=/usr/bin:/bin"}
	return cmd
}

func TestArgvAndNoShellExpansion(t *testing.T) {
	home := setup(t, map[string]any{"command": "/usr/bin/printf", "args": []string{"<%s>\n"}, "interactiveArgs": []string{"interactive"}, "execArgs": []string{"exec"}})
	for _, mode := range []string{"interactive", "exec"} {
		for _, separator := range []bool{false, true} {
			args := []string{"tool"}
			if separator {
				args = append(args, "--")
			}
			args = append(args, "--help", "--version", "--dry-run", "--show-args", "--json", "a b", "$(echo wrong)", "中文", "--")
			if mode == "exec" {
				args = append([]string{"exec"}, args...)
			}
			out, err := command(home, args...).CombinedOutput()
			want := "<" + mode + ">\n<--help>\n<--version>\n<--dry-run>\n<--show-args>\n<--json>\n<a b>\n<$(echo wrong)>\n<中文>\n<-->\n"
			if err != nil || string(out) != want {
				t.Fatalf("mode=%s separator=%t: %v: %s; want %s", mode, separator, err, out, want)
			}
		}
	}
}

func TestWorkingDirectoryEnvironmentAndInput(t *testing.T) {
	dir := t.TempDir()
	home := setup(t, map[string]any{"command": "/bin/sh", "execArgs": []string{"-c", `printf '%s\n' "$CHECK_VALUE"; pwd; cat`}, "env": map[string]string{"CHECK_VALUE": "${HOME}/value"}, "cwd": dir})
	cmd := command(home, "exec", "tool")
	cmd.Stdin = strings.NewReader("piped input\n")
	out, err := cmd.CombinedOutput()
	realDir, _ := filepath.EvalSymlinks(dir)
	if err != nil || string(out) != home+"/value\n"+realDir+"\npiped input\n" {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestInvalidConfigurationAndMissingEnvironment(t *testing.T) {
	for _, config := range []map[string]any{
		{"command": "/bin/echo", "unknown": true},
		{"command": "/bin/echo", "env": map[string]string{"TOKEN": "${MISSING_VALUE}"}},
		{"command": "missing-command"},
	} {
		home := setup(t, config)
		if out, err := command(home, "tool").CombinedOutput(); err == nil {
			t.Fatalf("unexpected success: %s", out)
		}
	}
	home := setup(t, map[string]any{"command": "/bin/echo"})
	if out, err := command(home, "exec", "tool").CombinedOutput(); err == nil || !bytes.Contains(out, []byte("execArgs")) {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := command(home, "../tool").CombinedOutput(); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestExitCodeAndSignal(t *testing.T) {
	home := setup(t, map[string]any{"command": "/bin/sh", "execArgs": []string{"-c"}})
	err := command(home, "exec", "tool", "exit 37").Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 37 {
		t.Fatalf("exit status lost: %v", err)
	}
	// 由真实进程向自身发送信号，验证启动器没有把信号转换成普通退出码。
	err = command(home, "exec", "tool", "kill -TERM $$").Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("signal lost: %v", err)
	}
}

func TestReadOnlyCommandsDoNotLaunch(t *testing.T) {
	home := setup(t, map[string]any{"command": "/bin/sleep", "args": []string{"30"}, "env": map[string]string{"TOKEN": "${MISSING_VALUE}"}})
	for _, args := range [][]string{{"profile", "list"}, {"profile", "check"}, {"profile", "show", "tool"}} {
		cmd := command(home, args...)
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	if out, err := command(home, "doctor", "tool").CombinedOutput(); err == nil || !bytes.Contains(out, []byte("MISSING_VALUE")) {
		t.Fatalf("%v: %s", err, out)
	}
}
