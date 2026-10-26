package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// ResourceHeaders is what the #NCPU:, #MEM:, #TIME: and #GPU: headers of a script ask for.
//   - A field a script does not set is zero or nil.
//   - Lines maps each header key that was set to the line it was written on.
type ResourceHeaders struct {
	CpusPerTask  int
	MemPerNodeMB int64
	Time         time.Duration
	Gpu          *GpuSpec
	Lines        map[string]int
}

// ParseResourceHeaders reads the resource headers from a script's annotations.
//   - An empty value is the same as no header.
//   - #MEM: with a bare number is in GB, and #TIME: with a bare number is in hours.
//   - A value that does not parse, or a key written twice, is an error naming its line.
//   - What did parse is returned with the error.
func ParseResourceHeaders(annotations []utils.Annotation) (ResourceHeaders, error) {
	h := ResourceHeaders{Lines: map[string]int{}}
	var problems []error
	for _, a := range annotations {
		switch a.Key {
		case "#NCPU", "#MEM", "#TIME", "#GPU":
		default:
			continue
		}
		if a.Value == "" {
			continue
		}
		if first, seen := h.Lines[a.Key]; seen {
			problems = append(problems, fmt.Errorf("%s on line %d: already set on line %d", a.Key, a.Line, first))
			continue
		}
		if err := h.set(a); err != nil {
			problems = append(problems, err)
			continue
		}
		h.Lines[a.Key] = a.Line
	}
	return h, errors.Join(problems...)
}

func (h *ResourceHeaders) set(a utils.Annotation) error {
	reject := func(want string) error {
		return fmt.Errorf("%s on line %d: %q is not %s", a.Key, a.Line, a.Value, want)
	}
	switch a.Key {
	case "#NCPU":
		n, err := strconv.Atoi(a.Value)
		if err != nil || n <= 0 {
			return reject("a positive whole number")
		}
		h.CpusPerTask = n
	case "#MEM":
		mb, err := utils.ParseMemoryMBWithDefault(a.Value, "GB")
		if err != nil || mb <= 0 {
			return reject("a memory size such as 8G")
		}
		h.MemPerNodeMB = mb
	case "#TIME":
		raw := a.Value
		if _, err := strconv.Atoi(raw); err == nil {
			raw += "h"
		}
		d, err := utils.ParseWalltime(raw)
		if err != nil || d <= 0 {
			return reject("a walltime such as 4h or 01:30:00")
		}
		h.Time = d
	case "#GPU":
		gpu, err := ParseGPU(a.Value)
		if err != nil {
			return reject("a GPU request such as 1, a100 or a100:2")
		}
		h.Gpu = gpu
	}
	return nil
}
