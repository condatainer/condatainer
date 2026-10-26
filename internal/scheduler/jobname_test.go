package scheduler

import (
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestParseJobName(t *testing.T) {
	for _, ok := range []string{"quant", "cnt-b-star-2.7.11b", " trimmed ", "a_b"} {
		if _, err := ParseJobName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "two words", "a/b", "q'x", "a[1-3]"} {
		if _, err := ParseJobName(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if got := SanitizeJobName("star/2.7.11b@abc 1"); got != "star-2.7.11b-abc-1" {
		t.Errorf("sanitized = %q", got)
	}
}

func TestHeaderNameIsChecked(t *testing.T) {
	if _, err := ParseResourceHeaders(utils.ScanAnnotations([]byte("#!/bin/bash\n#NAME: two words\n"))); err == nil {
		t.Error("a name with a space was accepted")
	}
}
