package cmd

import "testing"

// A prefix with or without the format's extension writes one file, and one with no file name is refused.
func TestExportPath(t *testing.T) {
	for _, tc := range []struct {
		prefix, ext, want string
	}{
		{"out/env", ".yml", "out/env.yml"},
		{"out/env.yml", ".yml", "out/env.yml"},
		{"out/env.txt", ".yml", "out/env.txt.yml"},
		{"out/env", ".txt", "out/env.txt"},
		{"out/.yml", ".yml", ""},
		{".yml", ".yml", ""},
		{"out/", ".yml", ""},
	} {
		got, err := exportPath(tc.prefix, tc.ext)
		if tc.want == "" {
			if err == nil {
				t.Errorf("exportPath(%q, %q) = %q, want an error", tc.prefix, tc.ext, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("exportPath(%q, %q) = (%q, %v), want %q", tc.prefix, tc.ext, got, err, tc.want)
		}
	}
}
