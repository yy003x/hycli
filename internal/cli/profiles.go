package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yy003x/hycli/internal/profile"
)

func listProfiles(dir string, stdout io.Writer) error {
	ids, err := profile.IDs(dir)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := fmt.Fprintln(stdout, id); err != nil {
			return err
		}
	}
	return nil
}

func showProfile(dir, id string, stdout io.Writer) error {
	p, err := profile.Load(dir, id)
	if err != nil {
		return err
	}
	// 展示声明，不解析环境变量，避免把已解析凭据打印到终端。
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(p)
}

func checkProfiles(dir, id string, stdout io.Writer) error {
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
	var failures []string
	for _, id := range ids {
		if _, err := profile.Load(dir, id); err != nil {
			failures = append(failures, id+": "+err.Error())
		} else if _, err := fmt.Fprintf(stdout, "%s: ok\n", id); err != nil {
			return err
		}
	}
	if len(failures) != 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}
