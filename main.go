// Command kmctl is the command-line tool for working with crews and Kubemoot.
package main

import (
	"os"

	"github.com/kubemoot/kmctl/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
