package scheduler

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// ResourceHeaders is what the resource and job headers of a script ask for.
//   - A field a script does not set is zero, nil or empty.
//   - Lines maps each header key that was set to the line it was written on.
type ResourceHeaders struct {
	CpusPerTask  int
	MemPerNodeMB int64
	Time         time.Duration
	Gpu          *GpuSpec
	JobName      string
	Stdout       string
	Stderr       string
	Partition    string
	Account      string
	Lines        map[string]int
}

// resourceHeaderKeys are the headers ParseResourceHeaders reads.
var resourceHeaderKeys = []string{"#NCPU", "#MEM", "#TIME", "#GPU", "#NAME", "#OUTPUT", "#ERROR", "#PARTITION", "#ACCOUNT"}

// Requests reports whether the script asks for resources, a partition or an account, which makes it a job.
// A name or a log path alone does not.
func (h ResourceHeaders) Requests() bool {
	for _, key := range []string{"#NCPU", "#MEM", "#TIME", "#GPU", "#PARTITION", "#ACCOUNT"} {
		if _, ok := h.Lines[key]; ok {
			return true
		}
	}
	return false
}

// ParseResourceHeaders reads the resource and job headers from a script's annotations.
//   - An empty value is the same as no header.
//   - #MEM: with a bare number is in GB, and #TIME: with a bare number is in hours.
//   - A value that does not parse, or a key written twice, is an error naming its line.
//   - What did parse is returned with the error.
func ParseResourceHeaders(annotations []utils.Annotation) (ResourceHeaders, error) {
	h := ResourceHeaders{Lines: map[string]int{}}
	var problems []error
	for _, a := range annotations {
		if !slices.Contains(resourceHeaderKeys, a.Key) {
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
	case "#NAME":
		name, err := ParseJobName(a.Value)
		if err != nil {
			return reject("a name of " + JobNameChars)
		}
		h.JobName = name
	case "#OUTPUT":
		h.Stdout = a.Value
	case "#ERROR":
		h.Stderr = a.Value
	case "#PARTITION":
		h.Partition = a.Value
	case "#ACCOUNT":
		h.Account = a.Value
	}
	return nil
}

// parseHeaderScript reads a script's resource headers as the specs of a job.
//   - It returns nil when the script has no header that asks for resources, a partition or an account.
//   - A header is written for no scheduler in particular, so the result carries the host's type and nothing is translated.
//   - A submit description file has its own resource lines and is not read here.
func parseHeaderScript(path string, host SchedulerType) (*ParsedScript, error) {
	if strings.HasSuffix(path, ".sub") {
		return nil, nil
	}
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	annotations := utils.ScanAnnotations([]byte(strings.Join(lines, "\n")))
	headers, err := ParseResourceHeaders(annotations)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidHeader, path, err)
	}
	if !headers.Requests() {
		return nil, nil
	}

	spec := GetSpecDefaults()
	spec.Nodes, spec.TasksPerNode = 1, 1
	spec.Override(&ResourceSpec{CpusPerTask: headers.CpusPerTask, MemPerNodeMB: headers.MemPerNodeMB, Time: headers.Time, Gpu: headers.Gpu})
	var raw []string
	for _, a := range annotations {
		if _, ok := headers.Lines[a.Key]; ok && slices.Contains(resourceHeaderKeys, a.Key) {
			raw = append(raw, strings.TrimSpace(lines[a.Line-1]))
		}
	}
	return &ParsedScript{
		Specs: &ScriptSpecs{
			ScriptPath: path,
			Spec:       &spec,
			Control: RuntimeConfig{
				JobName:   headers.JobName,
				Stdout:    headers.Stdout,
				Stderr:    headers.Stderr,
				Partition: headers.Partition,
				Account:   headers.Account,
			},
			HasDirectives: true,
			RawFlags:      raw,
			ScriptType:    host,
		},
		ScriptType:  host,
		FromHeaders: true,
	}, nil
}

// headerSource names the #KEY: headers in a note.
const headerSource = "#KEY: headers"

var notedSources sync.Map

// noteIgnoredSources prints one note when a script holds resources in a source other than the one that was read.
//   - It returns parsed and err unchanged, so a caller returns it in one step.
func noteIgnoredSources(path string, parsed *ParsedScript, err error) (*ParsedScript, error) {
	if err != nil || parsed == nil || strings.HasSuffix(path, ".sub") {
		return parsed, err
	}
	if _, seen := notedSources.LoadOrStore(path, true); seen {
		return parsed, nil
	}
	lines, readErr := readFileLines(path)
	if readErr != nil {
		return parsed, nil
	}
	lines = lines[:utils.HeaderEnd(lines)]

	winner := fmt.Sprintf("%s directives", parsed.ScriptType)
	if parsed.FromHeaders {
		winner = headerSource
	}
	var ignored []string
	first := map[string]int{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		for prefix, label := range map[string]string{"#SBATCH": "SLURM directives", "#PBS": "PBS directives", "#BSUB": "LSF directives"} {
			if strings.HasPrefix(line, prefix) && first[label] == 0 {
				first[label] = i + 1
			}
		}
	}
	for _, a := range utils.ScanAnnotations([]byte(strings.Join(lines, "\n"))) {
		if slices.Contains([]string{"#NCPU", "#MEM", "#TIME", "#GPU", "#PARTITION", "#ACCOUNT"}, a.Key) && first[headerSource] == 0 {
			first[headerSource] = a.Line
		}
	}
	for _, label := range []string{headerSource, "SLURM directives", "PBS directives", "LSF directives"} {
		if line := first[label]; line > 0 && label != winner {
			ignored = append(ignored, fmt.Sprintf("%s on line %d", label, line))
		}
	}
	if len(ignored) > 0 {
		utils.PrintNote("%s: reading the %s; ignoring the %s", path, winner, strings.Join(ignored, ", "))
	}
	return parsed, nil
}
