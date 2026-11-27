// Package units reads and writes the memory sizes, durations and transfer speeds that configs, headers and output share.
package units

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FormatSizeInput writes a size in MB as text ParseSizeInput reads back, with a one-letter unit.
//   - It uses "G" when divisible by 1024, otherwise "M".
//   - Examples: 8192 → "8G", 1536 → "1536M".
func FormatSizeInput(mb int64) string {
	if mb%1024 == 0 {
		return fmt.Sprintf("%dG", mb/1024)
	}
	return fmt.Sprintf("%dM", mb)
}

// FormatHMS writes a duration as the HH:MM:SS a job script carries, with the seconds cut and the hours counting past 24.
func FormatHMS(d time.Duration) string {
	total := int64(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, total%3600/60, total%60)
}

// FormatTimeTable writes a duration for a table column: HH:MM:SS, with the hours counting past 24.
//   - Seven days is "168:00:00".
func FormatTimeTable(d time.Duration) string { return FormatHMS(d) }

// FormatTimeDetail writes a duration where there is room: HH:MM:SS under a day, D-HH:MM:SS from a day.
//   - Seven days is "7-00:00:00".
func FormatTimeDetail(d time.Duration) string {
	if d < 24*time.Hour {
		return FormatHMS(d)
	}
	days := int64(d.Hours()) / 24
	return fmt.Sprintf("%d-%s", days, FormatHMS(d-time.Duration(days)*24*time.Hour))
}

// FormatMemoryDetail writes memory in MB where there is room, in GiB: "31.2 GiB", "512 MiB".
func FormatMemoryDetail(mb int64) string { return FormatSize(mb << 20) }

// FormatMemoryTable writes memory in MB for a table column, in at most five characters: "2150M", "10.2G", "123G", "12.6T".
//   - Below 10 GB it is whole megabytes.
//   - From there it is gigabytes, then from 10 TB terabytes. Each has one decimal below 100 and none from 100. A whole value drops its ".0".
func FormatMemoryTable(mb int64) string {
	if mb < 10240 {
		return strconv.FormatInt(mb, 10) + "M"
	}
	value, unit := float64(mb)/1024, "G"
	if value >= 10240 {
		value, unit = value/1024, "T"
	}
	if value >= 100 {
		return strconv.FormatFloat(value, 'f', 0, 64) + unit
	}
	return strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0") + unit
}

// minSpeedWindow is the shortest span FormatSpeed quotes a rate over; less is noise.
const minSpeedWindow = 100 * time.Millisecond

// FormatSpeed renders bytes moved over elapsed as a rate, such as "45.2 MiB/s",
// and reports false when the span is too short or nothing moved — a retried
// transfer can withdraw what it had counted, so the difference can be negative.
func FormatSpeed(bytes int64, elapsed time.Duration) (string, bool) {
	if bytes <= 0 || elapsed < minSpeedWindow {
		return "", false
	}
	return FormatSize(int64(float64(bytes)/elapsed.Seconds())) + "/s", true
}

// FormatSize renders a byte count for display, in binary units to three
// significant figures. Artifacts are gigabytes, so a raw byte count is a number
// nobody reads.
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value, exp := float64(bytes), 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}
	suffix := [...]string{"B", "KiB", "MiB", "GiB", "TiB"}[exp]
	if value >= 100 {
		return fmt.Sprintf("%.0f %s", value, suffix)
	}
	if value >= 10 {
		return fmt.Sprintf("%.1f %s", value, suffix)
	}
	return fmt.Sprintf("%.2f %s", value, suffix)
}

// FormatDuration formats a duration dropping zero trailing components.
// Examples: 2h0m0s → "2h", 48h → "2d", 25h30m → "1d1h30m", 1h30m15s → "1h30m15s".
func FormatDuration(d time.Duration) string {
	total := int(d.Seconds())
	days := total / 86400
	hours := (total % 86400) / 3600
	mins := (total % 3600) / 60
	secs := total % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if secs > 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, "")
}

// ParseMemoryMBWithDefault reads a memory a scheduler wrote into MB, with that scheduler's unit for a bare number.
//   - When the string has an explicit unit suffix (G/GB/M/MB/K/KB/T/TB) it is used directly.
//   - For bare numbers the provided defaultUnit is applied (e.g. "KB", "MB", "GB").
//   - What a person types is read by ParseSizeInput.
func ParseMemoryMBWithDefault(memStr, defaultUnit string) (int64, error) {
	memStr = strings.ToUpper(strings.TrimSpace(memStr))

	var value int64
	var unit string

	n, err := fmt.Sscanf(memStr, "%d%s", &value, &unit)
	if err != nil && n == 0 {
		return 0, fmt.Errorf("invalid memory format: %s", memStr)
	}

	if unit == "" {
		unit = strings.ToUpper(strings.TrimSpace(defaultUnit))
	}

	switch unit {
	case "G", "GB":
		return value * 1024, nil
	case "M", "MB":
		return value, nil
	case "K", "KB":
		return value / 1024, nil
	case "T", "TB":
		return value * 1024 * 1024, nil
	default:
		return 0, fmt.Errorf("invalid memory unit %q in: %s", unit, memStr)
	}
}

// parseCompoundDuration parses Go-style duration strings with an optional integer days prefix.
//   - Converts "Nd" to "N*24h" and delegates to time.ParseDuration.
//   - Examples: "4d12h" → 108h, "2h30m", "1.5h", "1d2h30m45s".
func parseCompoundDuration(s string) (time.Duration, error) {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" {
		return 0, nil
	}
	if idx := strings.IndexByte(lower, 'd'); idx >= 0 {
		n, err := strconv.ParseInt(lower[:idx], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid time format: %s", s)
		}
		lower = fmt.Sprintf("%dh%s", n*24, lower[idx+1:])
	}
	d, err := time.ParseDuration(lower)
	if err != nil {
		return 0, fmt.Errorf("invalid time format: %s", s)
	}
	return d, nil
}

// timeInput is a time a person typed as numbers: an optional day count, then hours, minutes and seconds.
var timeInput = regexp.MustCompile(`^(?:(\d+)-)?(\d+)(?:\.(\d+))?(?::(\d+))?(?::(\d+))?$`)

// ParseTimeInput reads a job time a person typed. The first number is hours, after an optional D- for days.
//   - 12 is 12 h, 12:30 is 12 h 30 m, 12:30:15 adds seconds; 1-12, 1-12:30 and 1-12:30:15 add a day.
//   - A bare number may have a decimal: 1.5 is 1 h 30 m. Words work too: 4d12h, 2h30m, 90m, 1.5h.
//   - Empty is 0, unset. Zero, a negative time, minutes or seconds of 60 and over, and anything else are refused.
func ParseTimeInput(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	refuse := fmt.Errorf("%q is not a time: use 12 (hours), 12:30, 1-12:00:00 or 2h30m", s)
	var d time.Duration
	if m := timeInput.FindStringSubmatch(s); m != nil {
		days, hours, frac, mins, secs := m[1], m[2], m[3], m[4], m[5]
		if frac != "" && (mins != "" || days != "") {
			return 0, refuse
		}
		n := func(v string) int64 { x, _ := strconv.ParseInt(v, 10, 64); return x }
		if n(mins) >= 60 || n(secs) >= 60 {
			return 0, refuse
		}
		h, _ := strconv.ParseFloat(hours+"."+frac+"0", 64)
		d = time.Duration(n(days))*24*time.Hour + (time.Duration(h * float64(time.Hour))).Round(time.Second) +
			time.Duration(n(mins))*time.Minute + time.Duration(n(secs))*time.Second
	} else if strings.ContainsAny(strings.ToLower(s), "dhms") && !strings.ContainsAny(s, ":-") {
		var err error
		if d, err = parseCompoundDuration(s); err != nil {
			return 0, refuse
		}
	} else {
		return 0, refuse
	}
	if d <= 0 {
		return 0, refuse
	}
	return d, nil
}

// sizeInput is a size a person typed: a number with an optional decimal, then an optional unit.
var sizeInput = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*([a-z]*)$`)

// sizeUnitMB is how many MB one of each unit is. Every unit is binary, and the written forms are equal.
var sizeUnitMB = map[string]*big.Rat{
	"":  big.NewRat(1024, 1),
	"k": big.NewRat(1, 1024), "kb": big.NewRat(1, 1024), "kib": big.NewRat(1, 1024),
	"m": big.NewRat(1, 1), "mb": big.NewRat(1, 1), "mib": big.NewRat(1, 1),
	"g": big.NewRat(1024, 1), "gb": big.NewRat(1024, 1), "gib": big.NewRat(1024, 1),
	"t": big.NewRat(1<<20, 1), "tb": big.NewRat(1<<20, 1), "tib": big.NewRat(1<<20, 1),
}

// ParseSizeInput reads a size a person typed, such as a job's memory or an overlay's size, in MB. A bare number is GiB.
//   - A unit is K, M, G or T, also written with B or iB, in any case, after an optional space. Each is binary: G is 1024 MB.
//   - A decimal is allowed. The result is rounded up to whole MB, so 1.5 is 1536 and 0.3G is 308.
//   - Empty is 0, unset. Zero, a negative size and any other unit are refused.
func ParseSizeInput(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	refuse := fmt.Errorf("%q is not a size: use 16 (GiB), 1.5G, 512M or 2T", s)
	m := sizeInput.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return 0, refuse
	}
	unit, ok := sizeUnitMB[m[2]]
	value, okValue := new(big.Rat).SetString(m[1])
	if !ok || !okValue {
		return 0, refuse
	}
	mb := value.Mul(value, unit)
	whole := new(big.Int).Quo(mb.Num(), mb.Denom())
	if new(big.Rat).SetInt(whole).Cmp(mb) < 0 {
		whole.Add(whole, big.NewInt(1))
	}
	if whole.Sign() <= 0 || !whole.IsInt64() {
		return 0, refuse
	}
	return whole.Int64(), nil
}
