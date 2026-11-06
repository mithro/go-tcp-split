// Command tcp-split is a split-TCP relay daemon.
package main

import (
	"os"

	"github.com/mithro/go-tcp-split/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
