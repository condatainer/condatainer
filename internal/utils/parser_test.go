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

func TestFormatMemoryTable(t *testing.T) {
	tests := map[int64]string{
		0:        "0M",
		512:      "512M",
		2150:     "2150M",
		10239:    "10239M",
		10240:    "10G",
		10445:    "10.2G",
		31949:    "31.2G",
		64512:    "63G",
		101990:   "99.6G",
		102400:   "100G",
		126362:   "123G",
		766000:   "748G",
		2048000:  "2000G",
		3045514:  "2974G",
		10485759: "10240G",
		10485760: "10T",
		11010048: "10.5T",
		13212057: "12.6T",
		12582912: "12T",
		2 << 30:  "2048T",
	}
	for mb, want := range tests {
		if got := FormatMemoryTable(mb); got != want {
			t.Errorf("FormatMemoryTable(%d) = %q, want %q", mb, got, want)
		}
	}
}

func TestFormatHMS(t *testing.T) {
	tests := map[time.Duration]string{
		0:                                     "00:00:00",
		59*time.Second + 900*time.Millisecond: "00:00:59",
		time.Hour + 30*time.Second:            "01:00:30",
		2*time.Hour + 30*time.Minute:          "02:30:00",
		4*time.Hour + 21*time.Minute + 49*time.Second: "04:21:49",
		72 * time.Hour:     "72:00:00",
		7 * 24 * time.Hour: "168:00:00",
	}
	for d, want := range tests {
		if got := FormatHMS(d); got != want {
			t.Errorf("FormatHMS(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFormatTimeDetail(t *testing.T) {
	tests := map[time.Duration]string{
		0:                             "00:00:00",
		23*time.Hour + 59*time.Minute: "23:59:00",
		24 * time.Hour:                "1-00:00:00",
		72 * time.Hour:                "3-00:00:00",
		7 * 24 * time.Hour:            "7-00:00:00",
		30*24*time.Hour + 5*time.Hour + 6*time.Second: "30-05:00:06",
	}
	for d, want := range tests {
		if got := FormatTimeDetail(d); got != want {
			t.Errorf("FormatTimeDetail(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFormatTimeTable(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "00:00:00", 72 * time.Hour: "72:00:00", 7 * 24 * time.Hour: "168:00:00"} {
		if got := FormatTimeTable(d); got != want {
			t.Errorf("FormatTimeTable(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFormatMemoryDetail(t *testing.T) {
	for mb, want := range map[int64]string{400: "400 MiB", 2150: "2.10 GiB", 31949: "31.2 GiB", 40 * 1024: "40.0 GiB"} {
		if got := FormatMemoryDetail(mb); got != want {
			t.Errorf("FormatMemoryDetail(%d) = %q, want %q", mb, got, want)
		}
	}
}

func TestFormatMemoryMB(t *testing.T) {
	for mb, want := range map[int64]string{512: "512M", 1536: "1536M", 8192: "8G", 32768: "32G"} {
		if got := FormatMemoryMB(mb); got != want {
			t.Errorf("FormatMemoryMB(%d) = %q, want %q", mb, got, want)
		}
		if back, err := ParseMemoryMB(FormatMemoryMB(mb)); err != nil || back != mb {
			t.Errorf("FormatMemoryMB(%d) = %q reads back as %d, %v", mb, FormatMemoryMB(mb), back, err)
		}
	}
}
