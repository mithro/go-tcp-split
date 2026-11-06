// Package cli implements the tcp-split command line.
package cli

import (
	"flag"
	"fmt"
	"io"
)

// Version is the release version; the build overrides it via -ldflags -X.
var Version = "dev"

// Main runs tcp-split with args (excluding the program name) and returns the
// process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tcp-split", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version {
		fmt.Fprintf(stdout, "tcp-split %s\n", Version)
		return 0
	}
	return 0
}
