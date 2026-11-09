package main

import (
	"fmt"
	"os"

	"github.com/yy003x/hycli/internal/cli"
	"github.com/yy003x/hycli/internal/session"
)

func main() {
	var err error
	if len(os.Args) == 5 && os.Args[1] == "__session-worker" {
		err = session.RunWorker(os.Args[2], os.Args[3], os.Args[4])
	} else {
		err = cli.Run(os.Args[1:], os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hycli:", err)
		os.Exit(1)
	}
}
