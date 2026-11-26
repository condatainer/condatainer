package helper

import "testing"

// A saved mem or time is stored with its unit; any other key as given.
func TestNormalizeConfigValue(t *testing.T) {
	for _, c := range []struct{ key, in, want string }{
		{"mem", "16", "16G"},
		{"MEM", "1.5", "1536M"},
		{"mem", "16 GiB", "16G"},
		{"time", "12", "12h"},
		{"time", "1:30", "1h30m"},
		{"time", "1-12", "1d12h"},
		{"partition", "gpu", "gpu"},
	} {
		if got, err := NormalizeConfigValue(c.key, c.in); err != nil || got != c.want {
			t.Errorf("NormalizeConfigValue(%q, %q) = %q, %v; want %q", c.key, c.in, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"mem", "lots"}, {"mem", "0"}, {"time", "soon"}, {"time", "12:75"}} {
		if _, err := NormalizeConfigValue(c[0], c[1]); err == nil {
			t.Errorf("NormalizeConfigValue(%q, %q) is not an error", c[0], c[1])
		}
	}
}
