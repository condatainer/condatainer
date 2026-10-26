package scheduler

import (
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestParseGPU(t *testing.T) {
	for in, want := range map[string]GpuSpec{
		"2":       {Count: 2},
		"a100":    {Type: "a100", Count: 1},
		"A100:2":  {Type: "a100", Count: 2},
		" h100 ":  {Type: "h100", Count: 1},
		"a100: 4": {Type: "a100", Count: 4},
	} {
		got, err := ParseGPU(in)
		if err != nil || *got != want {
			t.Errorf("ParseGPU(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "-1", "a100:0", "a100:x", "a100:", ":2"} {
		if _, err := ParseGPU(bad); err == nil {
			t.Errorf("ParseGPU(%q) was accepted", bad)
		}
	}
}

func TestParseResourceHeaders(t *testing.T) {
	text := "#!/bin/bash\n#NCPU: 8 ## threads\n#MEM: 16\n#TIME: 2\n#GPU: a100:2\n#NAME: ignored\n"
	h, err := ParseResourceHeaders(utils.ScanAnnotations([]byte(text)))
	if err != nil {
		t.Fatal(err)
	}
	if h.CpusPerTask != 8 || h.MemPerNodeMB != 16*1024 || h.Time != 2*time.Hour || h.Gpu == nil || h.Gpu.Count != 2 {
		t.Errorf("headers = %+v", h)
	}
	if h.Lines["#NCPU"] != 2 || h.Lines["#GPU"] != 5 || len(h.Lines) != 4 {
		t.Errorf("lines = %v", h.Lines)
	}

	text = "#NCPU: many\n#MEM: 4G\n#TIME: soon\n#GPU: a100:0\n#NCPU: 2\n#MEM:\n"
	h, err = ParseResourceHeaders(utils.ScanAnnotations([]byte(text)))
	if err == nil {
		t.Fatal("bad values were accepted")
	}
	for _, want := range []string{"#NCPU on line 1", "#TIME on line 3", "#GPU on line 4"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if h.MemPerNodeMB != 4096 || h.CpusPerTask != 2 {
		t.Errorf("the headers that parsed were dropped: %+v", h)
	}

	_, err = ParseResourceHeaders(utils.ScanAnnotations([]byte("#MEM: 4G\n#MEM: 8G\n")))
	if err == nil || !strings.Contains(err.Error(), "#MEM on line 2: already set on line 1") {
		t.Errorf("a repeated header: %v", err)
	}
}
