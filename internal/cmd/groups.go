package cmd

import (
	"fmt"
	"strings"

	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/spf13/cobra"
)

// groupAnnotation marks a command that only groups subcommands (crew, fitness, ...).
const groupAnnotation = "kmctl.kubemoot.org/group"

// isGroup reports whether cmd only groups subcommands.
func isGroup(cmd *cobra.Command) bool { return cmd.Annotations[groupAnnotation] == "true" }

// markGroups turns every command that only groups subcommands into one that
// rejects an unknown subcommand: a mistyped "kmctl crew lst" is an error with a
// suggestion and a non-zero exit, not the group's help with exit 0. A group
// given no arguments still prints its help.
func markGroups(c *cobra.Command) {
	for _, sub := range c.Commands() {
		markGroups(sub)
	}
	if !c.HasParent() || c.Runnable() || !c.HasSubCommands() {
		return
	}
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[groupAnnotation] = "true"
	c.Args = cobra.ArbitraryArgs
	c.SuggestionsMinimumDistance = 2
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return unknownSubcommand(cmd, args[0])
	}
}

// unknownSubcommand is the error for a subcommand the group does not have.
func unknownSubcommand(group *cobra.Command, name string) error {
	msg := fmt.Sprintf("unknown command %q for %q", name, group.CommandPath())
	hint := "Available commands: " + strings.Join(subcommandNames(group), ", ") + "; see " + group.CommandPath() + " --help"
	if s := group.SuggestionsFor(name); len(s) > 0 {
		for i := range s {
			s[i] = group.CommandPath() + " " + s[i]
		}
		hint = "Did you mean: " + strings.Join(s, ", ") + "? (" + hint + ")"
	}
	return clierr.New(msg, hint, nil)
}

func subcommandNames(group *cobra.Command) []string {
	var names []string
	for _, sub := range group.Commands() {
		if sub.IsAvailableCommand() {
			names = append(names, sub.Name())
		}
	}
	return names
}
