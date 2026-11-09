package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yy003x/hycli/internal/launch"
	"github.com/yy003x/hycli/internal/profile"
)

type readiness struct {
	ID               string            `json:"id"`
	Source           string            `json:"source"`
	OK               bool              `json:"ok"`
	Executable       string            `json:"executable,omitempty"`
	CWD              string            `json:"cwd,omitempty"`
	SupportsExec     bool              `json:"supportsExec"`
	Environment      map[string]string `json:"environment,omitempty"`
	MissingVariables []string          `json:"missingVariables,omitempty"`
	Error            string            `json:"error,omitempty"`
}

var reference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func inspect(dir, id string) (readiness, profile.Profile) {
	r := readiness{ID: id, Source: filepath.Join(dir, id+".json")}
	p, err := profile.Load(dir, id)
	if err == nil {
		r.SupportsExec = p.ExecArgs != nil
		r.Environment = map[string]string{}
		missing := map[string]bool{}
		for key, value := range p.Env {
			refs := reference.FindAllString(value, -1)
			if len(refs) > 0 {
				r.Environment[key] = strings.Join(refs, ", ")
			} else {
				r.Environment[key] = "[literal hidden]"
			}
			for _, ref := range refs {
				name := ref[2 : len(ref)-1]
				if _, ok := os.LookupEnv(name); !ok {
					missing[name] = true
				}
			}
		}
		for name := range missing {
			r.MissingVariables = append(r.MissingVariables, name)
		}
		sort.Strings(r.MissingVariables)
		r.Executable, r.CWD, _, err = launch.Prepare(p)
	}
	r.OK = err == nil
	if err != nil {
		r.Error = err.Error()
	}
	return r, p
}

func doctor(dir, id string, jsonOutput bool, stdout io.Writer) error {
	ids := []string{id}
	if id == "" {
		var err error
		ids, err = profile.IDs(dir)
		if err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("no profiles in %s", dir)
	}
	results := []readiness{}
	failures := 0
	for _, id := range ids {
		r, _ := inspect(dir, id)
		results = append(results, r)
		if !r.OK {
			failures++
		}
	}
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(struct {
			SchemaVersion int         `json:"schemaVersion"`
			OK            bool        `json:"ok"`
			Profiles      []readiness `json:"profiles"`
		}{1, failures == 0, results}); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			if !r.OK {
				fmt.Fprintf(stdout, "%s: error: %s\n", r.ID, r.Error)
				continue
			}
			fmt.Fprintf(stdout, "%s: ok\n  source: %s\n  executable: %s\n  cwd: %s\n  exec supported: %t\n", r.ID, r.Source, r.Executable, r.CWD, r.SupportsExec)
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d profiles failed readiness checks", failures)
	}
	return nil
}

func dryRun(dir string, call invocation, stdout io.Writer) error {
	r, p := inspect(dir, call.id)
	if !r.OK {
		return fmt.Errorf("%s", r.Error)
	}
	argv, err := p.Argv(call.noninteractive, call.arguments)
	if err != nil {
		return err
	}
	if !call.showArgs {
		for i := range argv {
			argv[i] = "[argument hidden]"
		}
	}
	mode := "interactive"
	if call.noninteractive {
		mode = "exec"
	}
	return json.NewEncoder(stdout).Encode(struct {
		SchemaVersion    int       `json:"schemaVersion"`
		Profile          readiness `json:"profile"`
		Mode             string    `json:"mode"`
		Args             []string  `json:"args"`
		ArgumentsVisible bool      `json:"argumentsVisible"`
	}{1, r, mode, argv, call.showArgs})
}
