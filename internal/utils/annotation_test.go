package utils

import "testing"

// Annotations are found wherever they are written, so what qualifies as one is
// pinned exactly: position never disqualifies a line, and shape always can.
func TestScan(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Annotation
	}{
		{"a leading annotation", "#DESC: hello\n", []Annotation{{Key: "#DESC", Value: "hello", Line: 1}}},
		{"one below the first command", "echo hi\n#DEP: star/2.7\n",
			[]Annotation{{Key: "#DEP", Value: "star/2.7", Line: 2}}},
		{"one inside a heredoc", "cat <<'EOF'\n#DEP: star/2.7\nEOF\n",
			[]Annotation{{Key: "#DEP", Value: "star/2.7", Line: 2}}},
		{"indented", "  \t#DEP: star/2.7\n", []Annotation{{Key: "#DEP", Value: "star/2.7", Line: 1}}},
		{"a note is kept apart from the value", "#ENV: PATH={prefix}/bin  ## on PATH\n",
			[]Annotation{{Key: "#ENV", Value: "PATH={prefix}/bin", Note: "on PATH", Line: 1}}},
		{"a single # is part of the value", "#SOURCE: crx https://example.org/a#v2\n",
			[]Annotation{{Key: "#SOURCE", Value: "crx https://example.org/a#v2", Line: 1}}},
		{"## ends the value for any key", "#DEP: star/2.7  ## the aligner\n",
			[]Annotation{{Key: "#DEP", Value: "star/2.7", Note: "the aligner", Line: 1}}},
		{"a shebang is not an annotation", "#!/bin/bash\n", nil},
		{"lower-case prose is not an annotation", "# note: rerun weekly\n", nil},
		{"a bare key without '#' is not an annotation", "TYPE:app\n", nil},
		{"a comment with no colon is not an annotation", "#DESC no colon\n", nil},
		{"a directive is not an annotation", "#SBATCH --time=01:00:00\n", nil},
		{"CRLF", "#DESC: hello\r\n", []Annotation{{Key: "#DESC", Value: "hello", Line: 1}}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScanAnnotations([]byte(tt.text))
			if len(got) != len(tt.want) {
				t.Fatalf("Scan = %#v, want %#v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
