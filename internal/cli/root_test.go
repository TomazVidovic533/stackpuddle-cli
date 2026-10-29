package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommandHelp(t *testing.T) {
	t.Parallel()

	command := NewRootCommand(Dependencies{})
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"--help"})

	if err := command.Execute(); err != nil {
		t.Fatalf("execute root help: %v", err)
	}

	if command.Name() != "stackpuddle" {
		t.Fatalf("root command name = %q, want stackpuddle", command.Name())
	}
	if !strings.Contains(output.String(), "stackpuddle") {
		t.Fatalf("root help does not identify stackpuddle:\n%s", output.String())
	}
}
