package main

import (
	"fmt"
	"os"

	"github.com/yy003x/hycli/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hycli:", err)
		os.Exit(1)
	}
}
