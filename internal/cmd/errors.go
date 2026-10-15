package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// verboseFlag is the root flag that adds the underlying error to the message.
const verboseFlag = "verbose"

// report runs root and, when it fails, writes the explained error to stderr and
// returns exit code 1. Verbose output comes from --verbose or KMCTL_DEBUG.
func report(root *cobra.Command, f *client.Factory, stderr io.Writer, styled bool, getenv func(string) string) int {
	cmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	verbose, _ := root.PersistentFlags().GetBool(verboseFlag)
	format := clierr.Format{Verbose: verbose || clierr.Debug(getenv), Styled: styled}
	_ = clierr.Write(stderr, explain(err, cmd, f), format)
	return 1
}

// explain turns a command's error into the message kmctl prints: an explanation
// a command already gave, a usage mistake with the command's usage line, a named
// Kubemoot object that does not exist, or a Kubernetes client failure.
func explain(err error, cmd *cobra.Command, f *client.Factory) *clierr.Error {
	// An explanation a command gave comes first: its cause may itself be a 404
	// that missingObject would explain differently.
	if e, ok := clierr.As(err); ok {
		return e
	}
	if isUsageError(err) {
		return usageHint(cmd, err)
	}
	ns := f.Namespace()
	if e := missingObject(err, ns); e != nil {
		return e
	}
	return clierr.Explain(err, clierr.Env{Kubeconfig: kubeconfigFlag(f), Context: f.CurrentContext(), Namespace: ns})
}

// kubeconfigFlag is the --kubeconfig value, or empty when it was not passed.
func kubeconfigFlag(f *client.Factory) string {
	if f.ConfigFlags.KubeConfig == nil {
		return ""
	}
	return *f.ConfigFlags.KubeConfig
}

// usageError marks a mistake in the command line: a bad flag or the wrong number
// of arguments.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

// isUsageError reports whether err is a command-line mistake. Cobra reports an
// unknown command from the root without a type, so that one is told by its text;
// TestErrors_BadFlagsAndArgs pins the wording.
func isUsageError(err error) bool {
	var u usageError
	return errors.As(err, &u) || strings.HasPrefix(err.Error(), "unknown command ")
}

// usageHint keeps cobra's first line, which names the mistake, and points at the
// command's usage line and help.
func usageHint(cmd *cobra.Command, err error) *clierr.Error {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return clierr.New(msg, fmt.Sprintf("Usage: %s (see %s --help)", cmd.UseLine(), cmd.CommandPath()), err)
}

// markUsageErrors makes every command in the tree report flag and argument
// mistakes as usage errors. Subcommands inherit the root's flag error function.
func markUsageErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	markArgErrors(root)
}

// markArgErrors wraps the argument validator of c and its subcommands.
func markArgErrors(c *cobra.Command) {
	if args := c.Args; args != nil {
		c.Args = func(cmd *cobra.Command, a []string) error {
			if err := args(cmd, a); err != nil {
				return usageError{err}
			}
			return nil
		}
	}
	for _, sub := range c.Commands() {
		markArgErrors(sub)
	}
}

// kindNoun names a Kubemoot resource in messages, and the kmctl command that
// lists it.
type kindNoun struct{ name, command string }

// kindNouns maps each resource the noun commands read to its name and command.
var kindNouns = map[string]kindNoun{
	"crews":             {"crew", "crew"},
	"agents":            {"agent", "agent"},
	"promptmodules":     {"prompt module", "prompt"},
	"modelproviders":    {"model provider", "model"},
	"crewfitnesssuites": {"fitness suite", "fitness"},
}

// missingObject explains a 404 for a named Kubemoot object, or returns nil.
func missingObject(err error, namespace string) *clierr.Error {
	var se *apierrors.StatusError
	if !clierr.MissingObject(err) || !errors.As(err, &se) {
		return nil
	}
	d := se.Status().Details
	noun, ok := kindNouns[d.Kind]
	if !ok {
		return nil
	}
	return clierr.New(noun.notIn(d.Name, namespace), noun.findHint(), err)
}

// notIn says no object of this kind has the name in the namespace.
func (n kindNoun) notIn(name, namespace string) string {
	return fmt.Sprintf("no %s %q in namespace %q", n.name, name, namespace)
}

// findHint names the command that lists this kind in every namespace.
func (n kindNoun) findHint() string {
	return fmt.Sprintf("Find it in every namespace with: kmctl %s list -A", n.command)
}
