package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// -o means one thing across kmctl: the output format of a command that prints
// (table, yaml, json, ...). A command that writes a file or a directory names it
// with its own flag; the old -o/--output spelling still works for one release,
// prints a deprecation message, and is hidden from help.

// pathFlag is a flag that names where a command writes: a file or a directory.
type pathFlag struct {
	name string // the flag, without dashes: output-file or dir
	kind string // what it names: "file" or "directory"
}

var (
	outputFileFlag = pathFlag{"output-file", "file"}
	outputDirFlag  = pathFlag{"dir", "directory"}
)

// add registers --NAME bound to target, plus the deprecated -o/--output bound to
// the same variable.
func (p pathFlag) add(fl *pflag.FlagSet, target *string, def, usage string) {
	fl.StringVarP(target, "output", "o", "", "")
	_ = fl.MarkHidden("output")
	fl.StringVar(target, p.name, def, usage)
}

// check rejects "-": these commands write a whole file or directory, never a
// stream, and "-" would otherwise become a file or directory named "-".
func (p pathFlag) check(value string) error {
	if value == "-" {
		return usageError{fmt.Errorf("--%s cannot be -: this command writes a %s, not standard output", p.name, p.kind)}
	}
	return nil
}

// checkPath is for a RunE: it prints the deprecation message when the old -o
// spelling was used (cobra hides pflag's own message), then rejects "-".
func checkPath(cmd *cobra.Command, p pathFlag, value string) error {
	if cmd.Flags().Changed("output") {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Flag -o/--output is deprecated, use --%s (-o names the output format in kmctl); it will be removed in the next release\n", p.name)
	}
	return p.check(value)
}
