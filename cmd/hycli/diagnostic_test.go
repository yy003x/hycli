package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDoctorExplainsExecutableAndMissingEnvironment(t *testing.T) {
	home := setup(t, map[string]any{"command": "/bin/echo", "execArgs": []string{}, "env": map[string]string{"TOKEN": "${MISSING_TOKEN}", "PRIVATE": "literal-secret"}})
	cmd := command(home, "doctor", "--json", "tool")
	out, err := cmd.Output()
	if err == nil || !json.Valid(out) || !bytes.Contains(out, []byte("MISSING_TOKEN")) || bytes.Contains(out, []byte("literal-secret")) {
		t.Fatalf("%s %v", out, err)
	}
	home = setup(t, map[string]any{"command": "echo", "execArgs": []string{}})
	out, err = command(home, "doctor", "--json", "tool").Output()
	if err != nil || !bytes.Contains(out, []byte(`/echo"`)) || !bytes.Contains(out, []byte(`"supportsExec":true`)) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestDryRunMasksArgumentsAndDoesNotLaunch(t *testing.T) {
	work := t.TempDir()
	marker := filepath.Join(work, "launched")
	home := setup(t, map[string]any{"command": "/usr/bin/touch", "args": []string{marker}, "execArgs": []string{}, "cwd": work, "env": map[string]string{"TOKEN": "secret-value"}})
	for _, prefix := range [][]string{{"--dry-run"}, {"exec", "--dry-run"}} {
		args := append(append([]string{}, prefix...), "tool", "--", "private-prompt")
		out, err := command(home, args...).Output()
		if err != nil || !json.Valid(out) || bytes.Contains(out, []byte("private-prompt")) || bytes.Contains(out, []byte("secret-value")) || bytes.Contains(out, []byte(marker)) {
			t.Fatalf("%s %v", out, err)
		}
		args = append(append([]string{}, prefix...), "--show-args", "tool", "--", "visible-argument")
		out, err = command(home, args...).Output()
		if err != nil || !bytes.Contains(out, []byte("visible-argument")) || bytes.Contains(out, []byte("secret-value")) {
			t.Fatalf("%s %v", out, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("dry-run launched the target: %v", err)
		}
	}
}

func TestDryRunUsesSelectedModeAndPreservesToolOptions(t *testing.T) {
	home := setup(t, map[string]any{"command": "/bin/echo", "args": []string{"common"}, "interactiveArgs": []string{"interactive"}, "execArgs": []string{"exec"}})
	for _, mode := range []string{"interactive", "exec"} {
		args := []string{"--dry-run", "--show-args", "tool", "--", "--help", "--dry-run", "--"}
		if mode == "exec" {
			args = append([]string{"exec"}, args...)
		}
		out, err := command(home, args...).Output()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var result struct {
			Mode             string   `json:"mode"`
			Args             []string `json:"args"`
			ArgumentsVisible bool     `json:"argumentsVisible"`
		}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatal(err)
		}
		want := []string{"common", mode, "--help", "--dry-run", "--"}
		if result.Mode != mode || !result.ArgumentsVisible || !reflect.DeepEqual(result.Args, want) {
			t.Fatalf("unexpected launch details: %s", out)
		}
	}
	home = setup(t, map[string]any{"command": "/bin/echo"})
	if out, err := command(home, "exec", "--dry-run", "tool").CombinedOutput(); err == nil || !bytes.Contains(out, []byte("execArgs")) {
		t.Fatalf("dry-run must enforce exec support: %v: %s", err, out)
	}
}
