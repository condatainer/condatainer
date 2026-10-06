package utils

import (
	"reflect"
	"testing"
	"time"
)

func TestParseMemoryMB(t *testing.T) {
	tests := []struct {
		input  string
		wantMB int64
	}{
		{"8G", 8 * 1024},
		{"8GB", 8 * 1024},
		{"1024M", 1024},
		{"1024MB", 1024},
		{"4096K", 4},
		{"4096KB", 4},
		{"1T", 1024 * 1024},
		{"1TB", 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			mb, err := ParseMemoryMB(tt.input)
			if err != nil {
				t.Errorf("ParseMemoryMB(%q) error: %v", tt.input, err)
				return
			}
			if mb != tt.wantMB {
				t.Errorf("ParseMemoryMB(%q) = %d MB; want %d MB", tt.input, mb, tt.wantMB)
			}
		})
	}
}

func TestParseWalltime(t *testing.T) {
	hour := time.Hour
	min := time.Minute
	sec := time.Second
	day := 24 * time.Hour

	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		// Compound: Go-style with optional integer days
		{"4d12h", 4*day + 12*hour, false},
		{"2h30m", 2*hour + 30*min, false},
		{"3h", 3 * hour, false},
		{"3H", 3 * hour, false}, // case-insensitive
		{"90m", 90 * min, false},
		{"1.5h", 90 * min, false},
		{"1d2h30m45s", day + 2*hour + 30*min + 45*sec, false},
		{"4d", 4 * day, false},
		{"", 0, false},
		// Colon-separated
		{"01:30:00", hour + 30*min, false},
		{"1:30", hour + 30*min, false},
		{"02:30:00", 2*hour + 30*min, false},
		{"90", 90 * min, false}, // minutes only
		// D-HH:MM:SS
		{"1-12:00:00", day + 12*hour, false},
		{"2-06:00:00", 2*day + 6*hour, false},
		// Errors
		{"abc", 0, true},      // no valid unit letters
		{"1.5d", 0, true},     // fractional days not supported (integer only)
		{"bad:time", 0, true}, // letters in colon-separated path
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			dur, err := ParseWalltime(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseWalltime(%q): expected error, got %v", tt.input, dur)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWalltime(%q) unexpected error: %v", tt.input, err)
			}
			if dur != tt.want {
				t.Errorf("ParseWalltime(%q) = %v; want %v", tt.input, dur, tt.want)
			}
		})
	}
}

func TestStripInlineComment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"No comment", "--cpus-per-task=8", "--cpus-per-task=8"},
		{"With comment", "--cpus-per-task=8  # This is a comment", "--cpus-per-task=8"},
		{"Comment only", "# Just a comment", ""},
		{"Multiple hashes", "--mem=16G # First # Second", "--mem=16G"},
		{"Hash in value needs escaping", "foo=bar#baz", "foo=bar"},
		{"Whitespace around comment", "--time=02:00:00   #   Time limit  ", "--time=02:00:00"},
		{"Empty after hash", "value #", "value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripInlineComment(tt.input)
			if got != tt.want {
				t.Errorf("StripInlineComment(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSortVersionsDescending(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"integers", []string{"22", "26", "23", "25", "24"}, []string{"26", "25", "24", "23", "22"}},
		{"semver", []string{"2.7.11a", "2.7.11b", "2.7.10"}, []string{"2.7.11b", "2.7.11a", "2.7.10"}},
		{"semver-mixed-suffix", []string{"2.7.9a", "2.7.11b", "2.7.10"}, []string{"2.7.11b", "2.7.10", "2.7.9a"}},
		{"mixed", []string{"101", "75", "151"}, []string{"151", "101", "75"}},
		{"single", []string{"1.0"}, []string{"1.0"}},
		{"empty", []string{}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SortVersionsDescending(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SortVersionsDescending(%v) = %v; want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatSpeed(t *testing.T) {
	tests := []struct {
		name    string
		bytes   int64
		elapsed time.Duration
		want    string
	}{
		{"steady", 100 << 20, time.Second, "100 MiB/s"},
		{"fraction of a second", 50 << 20, 500 * time.Millisecond, "100 MiB/s"},
		{"too short to quote", 100 << 20, 10 * time.Millisecond, ""},
		{"nothing moved", 0, time.Second, ""},
		{"a retried transfer withdrew bytes", -5, time.Second, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FormatSpeed(tt.bytes, tt.elapsed)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("FormatSpeed(%d, %v) = %q, %v; want %q", tt.bytes, tt.elapsed, got, ok, tt.want)
			}
		})
	}
}
