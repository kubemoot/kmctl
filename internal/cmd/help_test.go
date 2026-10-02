package cmd

import (
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
)

// Every user-facing command should carry a runnable Example (help polish).
func TestCommandsHaveExamples(t *testing.T) {
	f := client.NewFactory()
	cmds := map[string]string{
		"root":        NewRootCommand().Example,
		"create":      newCreateCommand(f).Example,
		"apply":       newApplyCommand(f).Example,
		"list":        newListCommand(f, "crew", resource.Crew).Example,
		"get":         newGetCommand(f, "crew", resource.Crew).Example,
		"fitness run": newRunCommand(f).Example,
	}
	for name, example := range cmds {
		if example == "" {
			t.Errorf("command %q has no Example", name)
		}
	}
}

// get should offer dynamic name completion so `kmctl crew get <TAB>` works.
func TestGetCommand_HasCompletion(t *testing.T) {
	if newGetCommand(client.NewFactory(), "crew", resource.Crew).ValidArgsFunction == nil {
		t.Error("get command has no ValidArgsFunction for name completion")
	}
}
