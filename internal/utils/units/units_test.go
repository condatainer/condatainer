package units

import (
	"testing"
	"time"
)

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

func TestFormatSizeInput(t *testing.T) {
	for mb, want := range map[int64]string{512: "512M", 1536: "1536M", 8192: "8G", 32768: "32G"} {
		if got := FormatSizeInput(mb); got != want {
			t.Errorf("FormatSizeInput(%d) = %q, want %q", mb, got, want)
		}
		if back, err := ParseSizeInput(FormatSizeInput(mb)); err != nil || back != mb {
			t.Errorf("FormatSizeInput(%d) = %q reads back as %d, %v", mb, FormatSizeInput(mb), back, err)
		}
	}
}

// The first number of a typed time is hours, after an optional day count.
func TestParseTimeInput(t *testing.T) {
	h, m, s := time.Hour, time.Minute, time.Second
	for in, want := range map[string]time.Duration{
		"":           0,
		"12":         12 * h,
		" 12 ":       12 * h,
		"36":         36 * h,
		"1.5":        h + 30*m,
		"0.25":       15 * m,
		"12:30":      12*h + 30*m,
		"0:30":       30 * m,
		"12:30:15":   12*h + 30*m + 15*s,
		"1-12":       36 * h,
		"1-12:30":    36*h + 30*m,
		"1-12:30:15": 36*h + 30*m + 15*s,
		"7-00:00:00": 168 * h,
		"4d12h":      108 * h,
		"2h30m":      2*h + 30*m,
		"90m":        90 * m,
		"1.5h":       h + 30*m,
		"2H":         2 * h,
	} {
		got, err := ParseTimeInput(in)
		if err != nil || got != want {
			t.Errorf("ParseTimeInput(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"0", "0:00", "-2", "-2h", "12:60", "12:30:60", "1-1.5", "1.5:30", "2-", "1:2:3:4", "soon", "2 h", "1.5d"} {
		if got, err := ParseTimeInput(in); err == nil {
			t.Errorf("ParseTimeInput(%q) = %v, want an error", in, got)
		}
	}
}

// A typed size with no unit is GiB, every unit is binary in any spelling, and a decimal rounds up to whole MB.
func TestParseSizeInput(t *testing.T) {
	for in, want := range map[string]int64{
		"":         0,
		"16":       16 * 1024,
		"1.5":      1536,
		"16G":      16 * 1024,
		"16g":      16 * 1024,
		"16GB":     16 * 1024,
		"16gib":    16 * 1024,
		"16 GiB":   16 * 1024,
		"31.2 GiB": 31949,
		"0.3G":     308,
		"512M":     512,
		"512mib":   512,
		"1.5M":     2,
		"2T":       2 << 20,
		"1536K":    2,
		"1024KB":   1,
		"1k":       1,
	} {
		got, err := ParseSizeInput(in)
		if err != nil || got != want {
			t.Errorf("ParseSizeInput(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"0", "0G", "-1", "-1G", "16Gi", "16 GBs", "16P", "G", "1.5.5G", "lots"} {
		if got, err := ParseSizeInput(in); err == nil {
			t.Errorf("ParseSizeInput(%q) = %d, want an error", in, got)
		}
	}
}
