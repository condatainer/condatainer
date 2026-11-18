package utils

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

func TestSplitOptions(t *testing.T) {
	tests := []struct {
		key, text string
		head      string
		opts      string // name=value pairs joined by ';', sorted
		refused   bool
	}{
		{key: "#DEP", text: "star", head: "star"},
		{key: "#DEP", text: "star optional=true", head: "star", opts: "optional=true"},
		{key: "#DEP", text: "star optional=false", head: "star", opts: "optional=false"},

		// The first word is the value, even when it holds '='.
		{key: "#DEP", text: "star=2.7.11b", head: "star=2.7.11b"},
		{key: "#DEP", text: "star=2.7.11b optional=true", head: "star=2.7.11b", opts: "optional=true"},
		{key: "#DEP", text: "star/2.7.11b>=2.7.0 optional=true", head: "star/2.7.11b>=2.7.0", opts: "optional=true"},

		// A word that is not an option stays in the value.
		{key: "#DEP", text: "star >= 2.7", head: "star >= 2.7"},
		{key: "#DEP", text: "star >= 2.7 optional=true", head: "star >= 2.7", opts: "optional=true"},

		// The note is stripped first.
		{key: "#DEP", text: "star optional=true ## optional=false", head: "star", opts: "optional=true"},
		{key: "#DEP", text: "star ## colour=red", head: "star"},
		{key: "#DEP", text: "star##optional=true", head: "star"},

		// A header that declares no options is read as written.
		{key: "#ENV", text: "NAME={prefix}/x", head: "NAME={prefix}/x"},
		{key: "#ENV", text: "NAME=x optional=true", head: "NAME=x optional=true"},
		{key: "#PARAM", text: "KEY=default", head: "KEY=default"},
		{key: "#SOURCE", text: "https://example.org/pkg?list=2 a=b", head: "https://example.org/pkg?list=2 a=b"},

		// Refused.
		{key: "#DEP", text: "star optional=true optional=false", refused: true},
		{key: "#DEP", text: "star colour=red", refused: true},
		{key: "#DEP", text: "star a=b", refused: true},
		{key: "#DEP", text: "star optional=maybe", refused: true},
		{key: "#DEP", text: "star optional=", refused: true},
		{key: "#DEP", text: `star optional="true"`, refused: true},
		{key: "#DEP", text: "star optional=true colour=red", refused: true},
		{key: "#DEP", text: "star optional=true extra", refused: true},
		{key: "#DEP", text: "star optional", refused: true},
		{key: "#DEP", text: "star optional=true ' ## ' ## note", refused: true},
	}
	for _, tt := range tests {
		a := ScanAnnotations([]byte(tt.key + ":" + tt.text))
		if len(a) != 1 {
			t.Fatalf("%s %q is not one annotation", tt.key, tt.text)
		}
		head, opts, err := SplitOptions(a[0], OptionOptional)
		if tt.refused {
			if !errors.Is(err, ErrHeaderOption) {
				t.Errorf("%s %q: err = %v, want ErrHeaderOption", tt.key, tt.text, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s %q: %v", tt.key, tt.text, err)
			continue
		}
		if head != tt.head {
			t.Errorf("%s %q: head = %q, want %q", tt.key, tt.text, head, tt.head)
		}
		if got := joinOptions(opts); got != tt.opts {
			t.Errorf("%s %q: options = %q, want %q", tt.key, tt.text, got, tt.opts)
		}
	}
}

func joinOptions(o Options) string {
	pairs := make([]string, 0, len(o))
	for k, v := range o {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ";")
}

// A reader that accepts no options treats optional like any unknown option.
func TestSplitOptionsAcceptingNone(t *testing.T) {
	a := ScanAnnotations([]byte("#DEP: star optional=true"))[0]
	_, _, err := SplitOptions(a)
	if !errors.Is(err, ErrHeaderOption) || !strings.Contains(err.Error(), `"optional" is not an option`) ||
		!strings.Contains(err.Error(), "this reader takes no options") {
		t.Fatalf("err = %v, want optional refused as an unknown option", err)
	}
	if head, _, err := SplitOptions(ScanAnnotations([]byte("#DEP: star >= 2.7"))[0]); err != nil || head != "star >= 2.7" {
		t.Errorf("a plain value = %q, %v, want it unchanged", head, err)
	}
}

func TestSplitOptionsErrorNamesValidOptions(t *testing.T) {
	a := ScanAnnotations([]byte("#DEP: star colour=red"))[0]
	_, _, err := SplitOptions(a, OptionOptional)
	if err == nil || !strings.Contains(err.Error(), "valid options are optional") ||
		!strings.Contains(err.Error(), "line 1") {
		t.Fatalf("err = %v, want it to name the line and the valid options", err)
	}
}

func TestOptionsBool(t *testing.T) {
	if !(Options{"optional": "true"}).Bool("optional") {
		t.Error("true should read as set")
	}
	if (Options{"optional": "false"}).Bool("optional") || (Options)(nil).Bool("optional") {
		t.Error("false and absent should read as unset")
	}
}
