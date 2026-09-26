package cmd

import (
	"bytes"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
)

func TestDeleteCommand_ArgValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no args, no -f", []string{}},
		{"one arg", []string{"crew"}},
		{"three args", []string{"crew", "a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newDeleteCommand(client.NewFactory())
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Errorf("args %v: expected validation error, got nil", tc.args)
			}
		})
	}
}
