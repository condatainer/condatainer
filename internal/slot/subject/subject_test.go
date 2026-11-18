package subject

import (
	"errors"
	"testing"
)

func TestSubjectMatch(t *testing.T) {
	for _, tc := range []struct {
		value, name string
		want        string // the entry that matched, or "" for no match
	}{
		{"grch38", "grch38/genome/gencode", "grch38"},
		{"grch38", "grch38", "grch38"},
		{"grch38", "grch38_grcm39/genome/gencode", ""},
		{"grch38,grcm39", "grcm39/genome/gencode/49", "grcm39"},
		{"gtf/gencode/4", "gtf/gencode/4.1", "gtf/gencode/4"},
		{"gtf/gencode/4", "gtf/gencode/47", ""},
		{"gtf=gencode", "gtf/gencode/47", "gtf/gencode"},
		{"regex:^grch38/", "grch38/genome/gencode", "regex:^grch38/"},
		{"regex:^grch38/", "grch38_grcm39/genome", ""},
		{"regex:^grch38/", "xgrch38/genome", ""},
		{"regex:grch38", "hg19_grch38/genome", "regex:grch38"},
		{"regex:^grch38$", "grch38/genome", ""},
		{"regex:a{1,3}", "aa", "regex:a{1,3}"},
		{"/data/genome.sqf", "/data/genome.sqf", "/data/genome.sqf"},
	} {
		s, err := Parse(tc.value)
		if err != nil {
			t.Fatalf("%q: %v", tc.value, err)
		}
		got, ok := s.Match(tc.name)
		if ok != (tc.want != "") || got != tc.want {
			t.Errorf("%q against %q = %q, %v; want %q", tc.value, tc.name, got, ok, tc.want)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for _, value := range []string{"", "a,,b", "regex:", "regex:(", "regex:a[b"} {
		if _, err := Parse(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v, want ErrInvalid", value, err)
		}
	}
}
