package cmd

import (
	"sort"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/config"
)

func TestConfigValueCompletion(t *testing.T) {
	opts := configValueCompletion("build.compress_args")
	expected := config.CompressNames()
	for _, e := range expected {
		found := false
		for _, o := range opts {
			if o == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected completion option %q not present", e)
		}
	}
}

func TestGetConfigEnvVars(t *testing.T) {
	vars := getConfigEnvVars()
	// build expected slice from configKeyDefs
	expected := make([]string, 0, len(configKeyDefs))
	for key := range configKeyDefs {
		env := "CNT_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		expected = append(expected, env)
	}
	sort.Strings(expected)

	if len(vars) != len(expected) {
		t.Fatalf("got %d vars, expected %d", len(vars), len(expected))
	}
	for i, v := range vars {
		if v != expected[i] {
			t.Errorf("env var[%d] = %q, want %q", i, v, expected[i])
		}
	}
}
