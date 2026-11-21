package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfirmChange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		yes   bool
		ok    bool
	}{
		{"yes flag skips the question", "", true, true},
		{"y", "y\n", false, true},
		{"YES in capitals", "YES\n", false, true},
		{"n", "n\n", false, false},
		{"anything else", "sure\n", false, false},
		{"no input at all", "", false, false},
	} {
		cmd := &cobra.Command{Use: "x"}
		var errOut bytes.Buffer
		cmd.SetErr(&errOut)
		cmd.SetIn(strings.NewReader(tc.input))
		err := confirmChange(cmd, tc.yes, "Really?")
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if tc.yes == (errOut.Len() > 0) {
			t.Errorf("%s: question printed = %v", tc.name, errOut.Len() > 0)
		}
		if err != nil && !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}
