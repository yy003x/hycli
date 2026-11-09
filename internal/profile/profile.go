// Package profile 只解析外部进程配置，不理解模型、工具或会话。
package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Profile struct {
	Command         string            `json:"command"`
	Args            []string          `json:"args,omitempty"`
	InteractiveArgs []string          `json:"interactiveArgs,omitempty"`
	ExecArgs        *[]string         `json:"execArgs,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Cwd             string            `json:"cwd,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
var envPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)
var envKeyPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func ValidID(id string) bool { return idPattern.MatchString(id) }

func Load(dir, id string) (Profile, error) {
	var p Profile
	if !ValidID(id) {
		return p, fmt.Errorf("invalid profile ID %q", id)
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return p, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, fmt.Errorf("profile %s: %w", id, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("profile %s: expected one JSON object", id)
	}
	if p.Command == "" || strings.ContainsRune(p.Command, 0) {
		return p, fmt.Errorf("profile %s: command is required", id)
	}
	for key, value := range p.Env {
		if !envKeyPattern.MatchString(key) || strings.ContainsRune(value, 0) {
			return p, fmt.Errorf("profile %s: invalid environment entry %q", id, key)
		}
	}
	allArgs := append(append([]string{}, p.Args...), p.InteractiveArgs...)
	if p.ExecArgs != nil {
		allArgs = append(allArgs, (*p.ExecArgs)...)
	}
	for _, arg := range allArgs {
		if strings.ContainsRune(arg, 0) {
			return p, fmt.Errorf("profile %s: argument contains NUL", id)
		}
	}
	if strings.ContainsRune(p.Cwd, 0) {
		return p, fmt.Errorf("profile %s: cwd contains NUL", id)
	}
	return p, nil
}

func IDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Expand 只替换显式变量引用，不执行 shell；缺失变量必须在启动前报错。
func Expand(value string, env map[string]string) (string, error) {
	var missing string
	value = envPattern.ReplaceAllStringFunc(value, func(ref string) string {
		key := ref[2 : len(ref)-1]
		v, ok := env[key]
		if !ok {
			missing = key
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("environment variable %s is not set", missing)
	}
	return value, nil
}

func Environment(overrides map[string]string, inherited []string) ([]string, error) {
	base := map[string]string{}
	for _, item := range inherited {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			base[key] = value
		}
	}
	resolved := map[string]string{}
	for key, value := range overrides {
		v, err := Expand(value, base)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", key, err)
		}
		resolved[key] = v
	}
	for key, value := range resolved {
		base[key] = value
	}
	result := make([]string, 0, len(base))
	for key, value := range base {
		result = append(result, key+"="+value)
	}
	sort.Strings(result)
	return result, nil
}

func (p Profile) Argv(noninteractive bool, extra []string) ([]string, error) {
	args := append([]string{}, p.Args...)
	if noninteractive {
		if p.ExecArgs == nil {
			return nil, fmt.Errorf("profile does not define execArgs; noninteractive execution is unsupported")
		}
		args = append(args, (*p.ExecArgs)...)
	} else {
		args = append(args, p.InteractiveArgs...)
	}
	return append(args, extra...), nil
}
