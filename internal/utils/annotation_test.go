package utils

import "testing"

// Only the header block declares, and within it the shape of a line decides what
// qualifies.
func TestScan(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Annotation
	}{
		{"a leading annotation", "#DESC: hello\n", []Annotation{{Key: "#DESC", Value: "hello", Line: 1}}},
		{"one below the first command", "echo hi\n#DEP: star/2.7\n", nil},
		{"one inside a heredoc", "cat <<'EOF'\n#DEP: star/2.7\nEOF\n", nil},
		{"after a shebang, blanks and comments", "#!/bin/bash\n\n# note\n#DEP: star/2.7\n",
			[]Annotation{{Key: "#DEP", Value: "star/2.7", Line: 4}}},
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

func TestStrayAnnotations(t *testing.T) {
	text := []byte("#!/bin/bash\n#DEP: a/1\nset -e\n#DEP: b/2\n#TODO: later\n")
	got := StrayAnnotations(text, "#DEP")
	if len(got) != 1 || got[0].Value != "b/2" || got[0].Line != 4 {
		t.Errorf("StrayAnnotations = %#v", got)
	}
	if all := StrayAnnotations(text); len(all) != 2 {
		t.Errorf("without keys, StrayAnnotations = %#v, want both", all)
	}
}

func TestFlag(t *testing.T) {
	for text, want := range map[string]bool{"": false, "#WRITABLE:\n": true, "#WRITABLE: true\n": true, "#WRITABLE:  ## note\n": true} {
		if got, err := Flag(ScanAnnotations([]byte(text)), "#WRITABLE"); err != nil || got != want {
			t.Errorf("Flag(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
	for _, text := range []string{"#WRITABLE: false\n", "#WRITABLE: 1\n"} {
		if _, err := Flag(ScanAnnotations([]byte(text)), "#WRITABLE"); err == nil {
			t.Errorf("Flag(%q) was accepted", text)
		}
	}
}

func TestReject(t *testing.T) {
	annotations := ScanAnnotations([]byte("#NCPU: 4\n#NAME: job\n"))
	if err := Reject(annotations, "a helper script", "#OUTPUT", "#NAME"); err == nil || err.Error() != "#NAME on line 2: a helper script does not read it" {
		t.Errorf("Reject = %v", err)
	}
	if err := Reject(annotations, "a helper script", "#OUTPUT"); err != nil {
		t.Errorf("Reject of an absent key = %v", err)
	}
}
