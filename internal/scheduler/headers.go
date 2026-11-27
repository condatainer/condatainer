package scheduler

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/condatainer/condatainer/internal/slot/subject"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/condatainer/condatainer/internal/utils/units"
)

// ResourceHeaders is what the resource and job headers of a script ask for.
//   - A field a script does not set is zero, nil or empty.
//   - Lines maps each header key that was set to the line it was written on. A key with conditional lines maps to the line chosen, or the first when none matched.
//   - Conditions says how each header with conditional lines was settled.
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
	Conditions   []Condition
}

// Subjects says which overlay each slot is bound to, so a conditional header can choose its line.
type Subjects struct {
	// Declared lists the slots the script declares. A condition on any other name is refused.
	Declared []string
	// Bound maps a slot to the exact name of its overlay. A slot that is not in it is unbound and matches nothing.
	Bound map[string]string
	// Skip lists the headers a command-line flag already sets, such as #MEM for -m. No line of a conditional one is chosen, so none is refused for matching nothing.
	Skip []string
}

// Condition is how a header that has conditional lines was settled.
type Condition struct {
	Key   string
	Line  int
	Value string
	// Because names what matched: `genome matched grch38`, or `no condition matched` for the unconditional line.
	Because string
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

// ParseResourceHeaders reads the resource and job headers from a script's annotations, with no slot bound.
//   - A line with a condition is an error, since there is no slot to match.
//   - See ParseResourceHeadersFor for the rules.
func ParseResourceHeaders(annotations []utils.Annotation) (ResourceHeaders, error) {
	return parseHeaderKeys(annotations, resourceHeaderKeys, Subjects{})
}

// ParseResourceHeadersFor reads the resource and job headers, choosing among conditional lines by the slots in subjects.
//   - An empty value is the same as no header.
//   - #MEM: with a bare number is in GiB, and #TIME: with a bare number is in hours.
//   - A value that does not parse, or an unconditional key written twice, is an error naming its line.
//   - What did parse is returned with the error.
//   - A resource header may carry a slot as an option: `#MEM: 56G genome=grch38`. The first conditional line whose slots all match wins, and the unconditional line is used when none does.
func ParseResourceHeadersFor(annotations []utils.Annotation, subjects Subjects) (ResourceHeaders, error) {
	return parseHeaderKeys(annotations, resourceHeaderKeys, subjects)
}

// headerLine is one header line, its value without the conditions, and the conditions.
type headerLine struct {
	ann   utils.Annotation
	slots map[string]subject.Subject
}

func (l headerLine) conditional() bool { return len(l.slots) > 0 }

// matches reports whether every slot of the line is bound to a matching overlay, and what matched.
func (l headerLine) matches(subjects Subjects) (string, bool) {
	var because []string
	for _, name := range sortedKeys(l.slots) {
		bound, ok := subjects.Bound[name]
		if !ok {
			return "", false
		}
		entry, ok := l.slots[name].Match(bound)
		if !ok {
			return "", false
		}
		because = append(because, fmt.Sprintf("%s matched %s", name, entry))
	}
	return strings.Join(because, ", "), true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// readHeaderLine splits a resource header into its value and the slots it is conditional on.
//   - Only #NCPU:, #MEM:, #TIME: and #GPU: take conditions, and only on a slot the script declares.
//   - The value is checked, whether or not the line is chosen.
func readHeaderLine(a utils.Annotation, subjects Subjects) (headerLine, error) {
	line := headerLine{ann: a}
	if !slices.Contains(resourceOnlyHeaderKeys, a.Key) {
		return line, nil
	}
	head, opts, err := utils.SplitOptions(a, subjects.Declared...)
	if err != nil {
		return line, err
	}
	line.ann.Value = head
	if len(opts) > 0 {
		line.slots = map[string]subject.Subject{}
	}
	for name, value := range opts {
		subject, err := subject.Parse(value)
		if err != nil {
			return line, fmt.Errorf("%s on line %d: %s=%s: %w", a.Key, a.Line, name, value, err)
		}
		line.slots[name] = subject
	}
	var check ResourceHeaders
	return line, check.set(line.ann)
}

// parseHeaderKeys reads only the headers in keys. A header outside keys is not read, so a value it holds is never checked.
func parseHeaderKeys(annotations []utils.Annotation, keys []string, subjects Subjects) (ResourceHeaders, error) {
	h := ResourceHeaders{Lines: map[string]int{}}
	var problems []error
	var order []string
	lines := map[string][]headerLine{}
	plain := map[string]int{}
	for _, a := range annotations {
		if !slices.Contains(keys, a.Key) || a.Value == "" {
			continue
		}
		line, err := readHeaderLine(a, subjects)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !line.conditional() {
			if first, seen := plain[a.Key]; seen {
				problems = append(problems, fmt.Errorf("%s on line %d: already set on line %d", a.Key, a.Line, first))
				continue
			}
			plain[a.Key] = a.Line
		}
		if _, seen := lines[a.Key]; !seen {
			order = append(order, a.Key)
		}
		lines[a.Key] = append(lines[a.Key], line)
	}
	for _, key := range order {
		if slices.Contains(subjects.Skip, key) && len(conditionalLines(lines[key])) > 0 {
			continue
		}
		chosen, because := chooseLine(lines[key], subjects)
		if chosen == nil {
			problems = append(problems, noLineMatches(key, lines[key], subjects))
			continue
		}
		if len(conditionalLines(lines[key])) > 0 {
			h.Conditions = append(h.Conditions, Condition{Key: key, Line: chosen.ann.Line, Value: chosen.ann.Value, Because: because})
		}
		if err := h.set(chosen.ann); err != nil {
			problems = append(problems, err)
			continue
		}
		h.Lines[key] = chosen.ann.Line
	}
	return h, errors.Join(problems...)
}

// chooseLine picks the first conditional line that matches, else the unconditional line.
func chooseLine(lines []headerLine, subjects Subjects) (*headerLine, string) {
	var fallback *headerLine
	for i, l := range lines {
		if !l.conditional() {
			fallback = &lines[i]
			continue
		}
		if because, ok := l.matches(subjects); ok {
			return &lines[i], because
		}
	}
	if fallback != nil && len(conditionalLines(lines)) > 0 {
		return fallback, "no condition matched"
	}
	return fallback, ""
}

// noLineMatches is the error for a header whose conditional lines all fail and which has no unconditional line.
//   - It says what each slot the lines name is bound to.
func noLineMatches(key string, lines []headerLine, subjects Subjects) error {
	names := map[string]bool{}
	for _, l := range lines {
		for name := range l.slots {
			names[name] = true
		}
	}
	var state []string
	for _, name := range sortedKeys(names) {
		if bound, ok := subjects.Bound[name]; ok {
			state = append(state, fmt.Sprintf("%s is %s", name, bound))
		} else {
			state = append(state, name+" is not bound")
		}
	}
	tried := make([]string, 0, len(lines))
	for _, n := range conditionalLines(lines) {
		tried = append(tried, strconv.Itoa(n))
	}
	return fmt.Errorf("%s on line %s: no line matches (%s); add a %s line with no condition for the other cases",
		key, strings.Join(tried, ", "), strings.Join(state, ", "), key)
}

func conditionalLines(lines []headerLine) []int {
	var out []int
	for _, l := range lines {
		if l.conditional() {
			out = append(out, l.ann.Line)
		}
	}
	return out
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
		mb, err := units.ParseSizeInput(a.Value)
		if err != nil || mb <= 0 {
			return reject("a memory size such as 16 (GiB) or 8G")
		}
		h.MemPerNodeMB = mb
	case "#TIME":
		d, err := units.ParseTimeInput(a.Value)
		if err != nil || d <= 0 {
			return reject("a walltime such as 4 (hours), 4h or 01:30:00")
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
func parseHeaderScript(path string, host SchedulerType, subjects Subjects) (*ParsedScript, error) {
	if strings.HasSuffix(path, ".sub") {
		return nil, nil
	}
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	annotations := utils.ScanAnnotations([]byte(strings.Join(lines, "\n")))
	headers, err := parseHeaderKeys(annotations, resourceHeaderKeys, subjects)
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
			Conditions:    headers.Conditions,
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
		if line, ok := h.Lines[a.Key]; ok && line == a.Line && slices.Contains(keys, a.Key) {
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
func withHeaders(path string, parsed *ParsedScript, err error, subjects Subjects) (*ParsedScript, error) {
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
	headers, err := parseHeaderKeys(annotations, keys, subjects)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidHeader, path, err)
	}
	if err := mergeJobControl(path, parsed, headers); err != nil {
		return nil, err
	}
	if specs.NativeResources {
		if err := refuseConditionalBesideNative(path, parsed, annotations); err != nil {
			return nil, err
		}
		warnIgnoredResourceHeaders(path, parsed, annotations)
		return parsed, nil
	}
	if headers.SetsResources() {
		spec := headerSpec(headers)
		specs.Spec = &spec
		specs.Conditions = headers.Conditions
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

// refuseConditionalBesideNative refuses a conditional resource header in a script whose directives set resources.
//   - Ignoring the header would make its condition do nothing without saying so.
func refuseConditionalBesideNative(path string, parsed *ParsedScript, annotations []utils.Annotation) error {
	for _, a := range annotations {
		if slices.Contains(resourceOnlyHeaderKeys, a.Key) && len(utils.OptionNames(a)) > 0 {
			return fmt.Errorf("%w: %s: %s on line %d has a condition, but the %s directives set resources; move the resources into the headers",
				ErrInvalidHeader, path, a.Key, a.Line, parsed.ScriptType)
		}
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
