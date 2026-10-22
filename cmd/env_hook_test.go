package cmd

import (
	"bytes"
	"testing"
)

func TestEnvHookOutsideAContainerPrintsNothing(t *testing.T) {
	t.Setenv("IN_CONDATAINER", "")
	t.Setenv("CNT_CONDA_ROOT", "")
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetErr(nil) })
	rootCmd.SetArgs([]string{"env", "hook", "--shell", "bash"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("env hook failed: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("env hook printed stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}
