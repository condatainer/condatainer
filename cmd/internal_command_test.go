package cmd

import "testing"

func TestInternalCommandsAreTheOnesUnderAnUnderscoreName(t *testing.T) {
	for path, want := range map[string][]string{
		"_helper pick-port": {"_helper", "pick-port"},
		"_server_daemon":    {"_server_daemon"},
		"list":              {"list"},
		"helper run":        {"helper", "run"},
	} {
		c, _, err := rootCmd.Find(want)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got, expect := isInternalCommand(c), path[0] == '_'; got != expect {
			t.Errorf("%s: isInternalCommand = %v, want %v", path, got, expect)
		}
	}
}
