package cmd

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/spf13/cobra"
)

// confirmChange asks before a command does something that cannot be undone, unless
// the command was given --yes. The question goes to stderr and the answer is read
// from stdin, so output piped from the command stays clean. Anything but y or yes,
// including no input at all, aborts without changing anything.
func confirmChange(cmd *cobra.Command, yes bool, question string) error {
	if yes {
		return nil
	}
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", question); err != nil {
		return err
	}
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return clierr.New("aborted: nothing was changed",
		"Answer y to confirm, pass --yes to skip the question, or --dry-run to see what would change.", nil)
}

// addDryRunFlag registers --dry-run, which every command that changes state has.
func addDryRunFlag(cmd *cobra.Command, dryRun *bool) {
	cmd.Flags().BoolVar(dryRun, "dry-run", false, "Show what would change and change nothing")
}

// addYesFlag registers --yes, which skips the confirmation of a destructive command.
func addYesFlag(cmd *cobra.Command, yes *bool) {
	cmd.Flags().BoolVarP(yes, "yes", "y", false, "Do not ask for confirmation")
}
