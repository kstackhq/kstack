// Command designsync does the bookkeeping that keeps the Figma design and the
// webview's code in step (design/CLAUDE.md). It reads the manifest,
// design/components.json, and never talks to Figma: the design side reaches it
// as the stamps file, what design/scripts/read-stamps.js returns.
// Run it from tools/: go run ./designsync <command> ....
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const usage = `usage: designsync <command> <args>

  check                            fail on an entry that is malformed or names a missing file or symbol
  status [-stamps <file>]          list the components and screens whose code or design moved since their last sync
  record [-stamps <file>] <id>...  set each entry's fingerprints to the working tree's and the stamps file's
  format                           rewrite the manifest in the tool's format`

// A tool runs the commands against one checkout.
type tool struct {
	root string // the repository's root
}

func main() {
	t := tool{root: ".."}
	if err := t.command(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "designsync:", err)
		os.Exit(1)
	}
}

func (t tool) command(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	stampsPath := flags.String("stamps", "", "")
	if err := flags.Parse(args[1:]); err != nil {
		return errors.New(usage)
	}
	rest := flags.Args()
	switch {
	case args[0] == "check" && len(args) == 1:
		return t.checkCmd(out)
	case args[0] == "status" && len(rest) == 0:
		return t.statusCmd(*stampsPath, out)
	case args[0] == "record" && len(rest) > 0:
		return t.recordCmd(*stampsPath, rest, out)
	case args[0] == "format" && len(args) == 1:
		return t.formatCmd()
	}
	return errors.New(usage)
}
