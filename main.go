// Command git-id mints, resolves and checks IDs that cross-reference documents in a git
// repository. Installed on PATH, it runs as `git id`.
package main

import (
	"fmt"
	"os"

	"github.com/creasty/git-id/internal/cli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git id: %v\n", err)
		os.Exit(2)
	}
	os.Exit(cli.Run(os.Args[1:], dir, os.Stdin, os.Stdout, os.Stderr))
}
