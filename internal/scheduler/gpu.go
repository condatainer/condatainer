package scheduler

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseGPU parses a GPU request: "N", "TYPE" or "TYPE:N".
//   - "a100:2" is two a100s, "a100" is one, and "2" is two of any type.
//   - The type is lower-cased.
//   - A count below 1, a missing type before ':', or an empty request is an error.
func ParseGPU(s string) (*GpuSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty GPU request")
	}
	if typ, count, found := strings.Cut(s, ":"); found {
		typ = strings.TrimSpace(typ)
		if typ == "" {
			return nil, fmt.Errorf("missing GPU type before ':' (expected TYPE:N, e.g. a100:2)")
		}
		n, err := strconv.Atoi(strings.TrimSpace(count))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid GPU count %q (expected TYPE:N, e.g. a100:2)", count)
		}
		return &GpuSpec{Type: strings.ToLower(typ), Count: n}, nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return nil, fmt.Errorf("GPU count must be > 0")
		}
		return &GpuSpec{Count: n}, nil
	}
	return &GpuSpec{Type: strings.ToLower(s), Count: 1}, nil
}
