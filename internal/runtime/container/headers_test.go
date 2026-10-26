package container

import (
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestParseHeaders(t *testing.T) {
	t.Setenv("CNT_TEST_DATA", "/scratch/data")
	text := "#!/bin/bash\n#BIND: $CNT_TEST_DATA:/data:ro\n#BIND: ${CNT_TEST_DATA}/ref:/ref\n#BIND:\n#WRITABLE:\n#FAKEROOT: true\n"
	h, err := ParseHeaders(utils.ScanAnnotations([]byte(text)))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Binds) != 2 || h.Binds[0] != "/scratch/data:/data:ro" || h.Binds[1] != "/scratch/data/ref:/ref" {
		t.Errorf("Binds = %q, want both expanded and the empty one skipped", h.Binds)
	}
	if !h.Writable || !h.Fakeroot {
		t.Errorf("flags = %+v, want both on", h)
	}

	h, err = ParseHeaders(utils.ScanAnnotations([]byte("#BIND: /a:/b\n")))
	if err != nil || h.Writable || h.Fakeroot || len(h.Binds) != 1 {
		t.Errorf("only a bind: %+v, %v", h, err)
	}

	_, err = ParseHeaders(utils.ScanAnnotations([]byte("#WRITABLE: false\n#FAKEROOT: no\n")))
	if err == nil || !strings.Contains(err.Error(), "#WRITABLE on line 1") || !strings.Contains(err.Error(), "#FAKEROOT on line 2") {
		t.Errorf("a flag with a value: %v", err)
	}
}
