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

// resourceOnlyHeaderKeys are the headers that say what the job needs. The rest name the job.
var resourceOnlyHeaderKeys = []string{"#NCPU", "#MEM", "#TIME", "#GPU"}

// jobControlHeaderKeys are the headers that name the job and where it goes.
var jobControlHeaderKeys = []string{"#NAME", "#OUTPUT", "#ERROR", "#PARTITION", "#ACCOUNT"}

// Requests reports whether the script has any resource or job header, which makes it a job.
func (h ResourceHeaders) Requests() bool {
	return len(h.Lines) > 0
}

// SetsResources reports whether the script says what the job needs, as against only naming it.
func (h ResourceHeaders) SetsResources() bool {
	for _, key := range resourceOnlyHeaderKeys {
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
	return parseHeaderKeys(annotations, resourceHeaderKeys)
}

// parseHeaderKeys reads only the headers in keys. A header outside keys is not read, so a value it holds is never checked.
func parseHeaderKeys(annotations []utils.Annotation, keys []string) (ResourceHeaders, error) {
	h := ResourceHeaders{Lines: map[string]int{}}
	var problems []error
	for _, a := range annotations {
		if !slices.Contains(keys, a.Key) {
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
//   - It returns nil when the script has no resource or job header.
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

	spec := headerSpec(headers)
	raw := headerRawLines(annotations, lines, headers, resourceHeaderKeys)
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

// headerSpec is the resource spec a script's resource headers ask for, on the defaults.
func headerSpec(h ResourceHeaders) ResourceSpec {
	spec := GetSpecDefaults()
	spec.Nodes, spec.TasksPerNode = 1, 1
	spec.Override(&ResourceSpec{CpusPerTask: h.CpusPerTask, MemPerNodeMB: h.MemPerNodeMB, Time: h.Time, Gpu: h.Gpu})
	return spec
}

// headerRawLines returns the lines of the headers in keys that were set, as written.
func headerRawLines(annotations []utils.Annotation, lines []string, h ResourceHeaders, keys []string) []string {
	var raw []string
	for _, a := range annotations {
		if _, ok := h.Lines[a.Key]; ok && slices.Contains(keys, a.Key) {
			raw = append(raw, strings.TrimSpace(lines[a.Line-1]))
		}
	}
	return raw
}

// withHeaders adds what a script's headers contribute to a source read from native directives.
//   - Job control from the headers joins the directives'. A field named in both is an error naming both.
//   - When no directive set a resource, the resource headers give the resources. The other directives still pass through to the job.
//   - When a directive set one, a resource header is ignored with a warning naming its line.
//   - A script whose directives did not parse, and a submit description file, are returned as read.
func withHeaders(path string, parsed *ParsedScript, err error) (*ParsedScript, error) {
	if err != nil || parsed == nil || parsed.Specs == nil || parsed.Specs.Spec == nil || strings.HasSuffix(path, ".sub") {
		return parsed, err
	}
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	annotations := utils.ScanAnnotations([]byte(strings.Join(lines, "\n")))
	specs := parsed.Specs

	keys := jobControlHeaderKeys
	if !specs.NativeResources {
		keys = resourceHeaderKeys
	}
	headers, err := parseHeaderKeys(annotations, keys)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidHeader, path, err)
	}
	if err := mergeJobControl(path, parsed, headers); err != nil {
		return nil, err
	}
	if specs.NativeResources {
		warnIgnoredResourceHeaders(path, parsed, annotations)
		return parsed, nil
	}
	if headers.SetsResources() {
		spec := headerSpec(headers)
		specs.Spec = &spec
		specs.RawFlags = append(specs.RawFlags, headerRawLines(annotations, lines, headers, resourceOnlyHeaderKeys)...)
		parsed.FromHeaders = true
	}
	return parsed, nil
}

// nativeFlagNames lists, per scheduler, the directive flags that set each job control field.
var nativeFlagNames = map[SchedulerType]struct {
	prefix string
	flags  map[string][]string
}{
	SchedulerSLURM: {"#SBATCH", map[string][]string{"#NAME": {"--job-name", "-J"}, "#OUTPUT": {"--output", "-o"}, "#ERROR": {"--error", "-e"}, "#PARTITION": {"--partition", "-p"}, "#ACCOUNT": {"--account", "-A"}}},
	SchedulerPBS:   {"#PBS", map[string][]string{"#NAME": {"-N"}, "#OUTPUT": {"-o"}, "#ERROR": {"-e"}, "#PARTITION": {"-q"}, "#ACCOUNT": {"-A"}}},
	SchedulerLSF:   {"#BSUB", map[string][]string{"#NAME": {"-J"}, "#OUTPUT": {"-o"}, "#ERROR": {"-e"}, "#PARTITION": {"-q"}, "#ACCOUNT": {"-P"}}},
}

// nativeFlagText is the directive that set a job control field, as written, or a plain description when it cannot be found.
func nativeFlagText(parsed *ParsedScript, key string) string {
	names, ok := nativeFlagNames[parsed.ScriptType]
	if !ok {
		return "a native directive"
	}
	for _, flag := range parsed.Specs.RawFlags {
		if flagMatches(flag, names.flags[key]...) {
			return names.prefix + " " + flag
		}
	}
	return "a native directive"
}

// mergeJobControl copies the job control the headers name into the specs.
//   - A field a directive already set, and a header names too, is an error naming both lines.
func mergeJobControl(path string, parsed *ParsedScript, h ResourceHeaders) error {
	c := &parsed.Specs.Control
	for _, f := range []struct {
		key, what string
		native    *string
		header    string
	}{
		{"#NAME", "job name", &c.JobName, h.JobName},
		{"#OUTPUT", "output path", &c.Stdout, h.Stdout},
		{"#ERROR", "error path", &c.Stderr, h.Stderr},
		{"#PARTITION", "partition", &c.Partition, h.Partition},
		{"#ACCOUNT", "account", &c.Account, h.Account},
	} {
		if f.header == "" {
			continue
		}
		if *f.native != "" {
			return fmt.Errorf("%w: %s: %s on line %d and %s both set the %s; keep one",
				ErrInvalidHeader, path, f.key, h.Lines[f.key], nativeFlagText(parsed, f.key), f.what)
		}
		*f.native = f.header
	}
	return nil
}

var warnedHeaders sync.Map

// warnIgnoredResourceHeaders warns once per script that its resource headers are ignored beside directives that set resources.
func warnIgnoredResourceHeaders(path string, parsed *ParsedScript, annotations []utils.Annotation) {
	var ignored []string
	for _, a := range annotations {
		if a.Value != "" && slices.Contains(resourceOnlyHeaderKeys, a.Key) {
			ignored = append(ignored, fmt.Sprintf("%s on line %d", a.Key, a.Line))
		}
	}
	if len(ignored) == 0 {
		return
	}
	if _, seen := warnedHeaders.LoadOrStore(path, true); seen {
		return
	}
	utils.PrintWarning("%s: ignoring %s; the %s directives set resources", path, strings.Join(ignored, ", "), parsed.ScriptType)
}

// headerSource names the #KEY: headers in a note.
const headerSource = "#KEY: headers"

var notedSources sync.Map

// noteIgnoredSources prints one note when a script holds directives of a scheduler other than the one that was read.
//   - The sources read are the native directives of the script's scheduler, the #KEY: headers, or both.
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

	var read []string
	if parsed.Native {
		read = append(read, fmt.Sprintf("%s directives", parsed.ScriptType))
	}
	if parsed.FromHeaders {
		read = append(read, headerSource)
	}
	first := map[string]int{}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		for prefix, label := range map[string]string{"#SBATCH": "SLURM directives", "#PBS": "PBS directives", "#BSUB": "LSF directives"} {
			if strings.HasPrefix(line, prefix) && first[label] == 0 {
				first[label] = i + 1
			}
		}
	}
	var ignored []string
	for _, label := range []string{"SLURM directives", "PBS directives", "LSF directives"} {
		if line := first[label]; line > 0 && !slices.Contains(read, label) {
			ignored = append(ignored, fmt.Sprintf("%s on line %d", label, line))
		}
	}
	if len(ignored) > 0 {
		utils.PrintNote("%s: reading the %s; ignoring the %s", path, strings.Join(read, " and "), strings.Join(ignored, ", "))
	}
	return parsed, nil
}
