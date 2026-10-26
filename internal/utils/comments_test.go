package utils

import "testing"

// Both keys hash the comment-stripped recipe, so this is the one derivation the
// whole design rests on.
func TestStripComments(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"the header goes with the rest", "#!/bin/bash\n#DESC:x\necho a\n", "echo a\n"},
		{"a whole-line comment goes", "echo a\n# note\necho b\n", "echo a\necho b\n"},
		{"an indented one goes", "echo a\n   \t# note\necho b\n", "echo a\necho b\n"},
		{"a heredoc's comment goes", "cat <<'EOF'\n#!/bin/bash\nexec x\nEOF\n", "cat <<'EOF'\nexec x\nEOF\n"},
		{"blank lines stay", "echo a\n\necho b\n", "echo a\n\necho b\n"},
		{"a trailing comment stays", "make -j2  # parallel\n", "make -j2  # parallel\n"},
		{"a # inside a string stays", "echo \"a # b\"\n", "echo \"a # b\"\n"},
		{"a ${x#y} expansion stays", "echo \"${v#pre}\"\n", "echo \"${v#pre}\"\n"},
		{"no trailing newline", "echo a\n# note", "echo a\n"},
		{"CRLF", "echo a\r\n# note\r\necho b\r\n", "echo a\r\necho b\r\n"},
		{"empty", "", ""},
		{"comments only", "# a\n# b\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(StripComments([]byte(tt.body))); got != tt.want {
				t.Errorf("StripComments = %q, want %q", got, tt.want)
			}
		})
	}
}
