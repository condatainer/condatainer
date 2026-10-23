package helper

import "testing"

func TestParseHelperParamLineKeys(t *testing.T) {
	cases := []struct {
		key string
		ok  bool
	}{
		{"PASSWORD", true},
		{"POSIT_R", true},
		{"_X1", true},
		{"password", false},
		{"1ABC", false},
		{"A-B", false},
		{"CNT_HELPER_PORT", false},
		{"NCPU", false},
		{"MEM_GB", false},
		{"OMP_NUM_THREADS", false},
		{"PATH", false},
		{"HOME", false},
		{"TMPDIR", false},
	}
	for _, c := range cases {
		_, err := parseHelperParamLine(c.key + `=x --opt "A param"`)
		if (err == nil) != c.ok {
			t.Errorf("key %q: err = %v, want ok = %v", c.key, err, c.ok)
		}
	}
}
