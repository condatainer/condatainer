package scheduler

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// slurmJobIDRe is a job ID `jobs` takes: a job, or one task of an array.
var slurmJobIDRe = regexp.MustCompile(`^[0-9]+(_[0-9]+)?$`)

// IsJobID reports whether arg is a SLURM job ID, such as "245068" or the array task "245068_4".
func (s *SlurmScheduler) IsJobID(arg string) bool { return slurmJobIDRe.MatchString(arg) }

// squeueField is a squeue -O field and the width it is printed in.
type squeueField struct {
	name  string
	width int
}

// squeueListFields are the fields every listing reads.
// A value is cut to its width, so each width is the longest value the field can hold in practice.
var squeueListFields = []squeueField{
	{"ArrayJobID", 20}, {"ArrayTaskID", 12}, {"Name", 128}, {"State", 24}, {"Reason", 48},
	{"Partition", 128}, {"NodeList", 512}, {"TimeUsed", 20}, {"TimeLimit", 20},
	{"tres-alloc", 200}, {"tres-per-node", 120}, {"tres-per-job", 120}, {"Dependency", 300},
	{"NumTasks", 12}, {"NTPerNode", 8}, {"cpus-per-task", 12},
}

// squeueFullFields are the fields added when a listing is read in full.
var squeueFullFields = []squeueField{
	{"SubmitTime", 24}, {"StartTime", 24}, {"EndTime", 24}, {"STDOUT", 1024}, {"STDERR", 1024},
}

// squeueFormat is the -O argument for fields. Each field has a width and ends in "|", which the
// reader checks to know that no value was shifted.
func squeueFormat(fields []squeueField) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = f.name + ":" + strconv.Itoa(f.width) + "|"
	}
	return strings.Join(parts, ",")
}

// squeueRecord is the values of one squeue line by field name. A value with no trailing padding
// filled its width and was cut, and its name is in cut.
type squeueRecord struct {
	value map[string]string
	cut   map[string]bool
}

// splitSqueueLine cuts a line at the widths of fields and checks the "|" that ends each.
func splitSqueueLine(line string, fields []squeueField) (squeueRecord, error) {
	rec := squeueRecord{value: make(map[string]string, len(fields)), cut: map[string]bool{}}
	pos := 0
	for _, f := range fields {
		end := pos + f.width
		if end >= len(line) || line[end] != '|' {
			return rec, fmt.Errorf("field %s is not %d wide: %q", f.name, f.width, line)
		}
		raw := line[pos:end]
		rec.value[f.name] = strings.TrimRight(raw, " ")
		rec.cut[f.name] = !strings.HasSuffix(raw, " ")
		pos = end + 1
	}
	return rec, nil
}

// slurmToolEnv is the environment of a call to a SLURM command. The user's defaults for that command,
// the variables that start with prefix, are removed: they filter, sort and reshape what the command
// does, and no option switches each of them off. standardTime sets the time format to the standard one.
// Variables that point at the cluster, such as SLURM_CONF, stay.
func slurmToolEnv(prefix string, standardTime bool) []string {
	env := commandEnv()
	if env == nil {
		env = os.Environ()
	}
	kept := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) || (standardTime && strings.HasPrefix(entry, "SLURM_TIME_FORMAT=")) {
			continue
		}
		kept = append(kept, entry)
	}
	if standardTime {
		kept = append(kept, "SLURM_TIME_FORMAT=standard")
	}
	return kept
}

// ListJobs lists the current user's jobs that wait or run with one squeue call.
// -r gives each array task its own row, so no task range is read.
func (s *SlurmScheduler) ListJobs(ctx context.Context, opts ListOptions) ([]JobInfo, error) {
	squeueBin := siblingBin(s.sbatchBin, "squeue")
	if squeueBin == "" {
		return nil, NewClusterError("SLURM", "list jobs", fmt.Errorf("squeue not found"))
	}
	fields := squeueListFields
	args := []string{"--me", "-r", "-h"}
	if opts.Full {
		fields = append(append([]squeueField{}, fields...), squeueFullFields...)
		args = append(args, "--expand-patterns")
	}
	args = append(args, "-O", squeueFormat(fields))

	out, err := runCommandEnv(ctx, slurmToolEnv("SQUEUE_", true), "SLURM", "list-jobs", squeueBin, args...)
	if err != nil {
		if _, ok := err.(*TimeoutError); ok {
			return nil, err
		}
		return nil, NewClusterError("SLURM", "list jobs", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
	}
	return parseSqueueJobs(string(out), fields)
}

// parseSqueueJobs reads the lines of a squeue call made with fields. A line that does not split is an error.
func parseSqueueJobs(output string, fields []squeueField) ([]JobInfo, error) {
	var jobs []JobInfo
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rec, err := splitSqueueLine(line, fields)
		if err != nil {
			return nil, NewClusterError("SLURM", "list jobs", err)
		}
		jobs = append(jobs, jobFromSqueue(rec))
	}
	return jobs, nil
}

// jobFromSqueue builds a JobInfo from one squeue record.
func jobFromSqueue(rec squeueRecord) JobInfo {
	v := rec.value
	j := JobInfo{ArrayID: v["ArrayJobID"], ID: v["ArrayJobID"], Name: v["Name"]}
	if task := v["ArrayTaskID"]; task != "" && task != "N/A" {
		j.TaskID = task
		j.ID = j.ArrayID + "_" + task
	} else {
		j.ArrayID = ""
	}

	j.Source = SourceQueue
	j.State, j.Suspended = slurmJobState(v["State"])
	setEnd(&j, v["State"], v["exit_code"])
	if reason := v["Reason"]; reason != "None" && reason != "(null)" {
		j.Reason = reason
	}
	for _, p := range strings.Split(v["Partition"], ",") {
		if p != "" {
			j.Partitions = append(j.Partitions, p)
		}
	}

	j.Cpus, j.MemMB, j.NodeCount, j.Gpus = parseSlurmTRES(v["tres-alloc"])
	if len(j.Gpus) == 0 {
		j.Gpus = requestedGpus(v["tres-per-job"], rec.cut["tres-per-job"], v["tres-per-node"], rec.cut["tres-per-node"], j.NodeCount)
	}
	if !rec.cut["NodeList"] {
		j.NodeList = expandSlurmNodeList(v["NodeList"])
		if j.NodeCount == 0 {
			j.NodeCount = len(j.NodeList)
		}
	}

	j.Elapsed = parseSlurmTime(v["TimeUsed"])
	switch limit := v["TimeLimit"]; limit {
	case "UNLIMITED":
		j.TimeUnlimited = true
	case "NOT_SET", "INVALID":
	default:
		j.TimeLimit = parseSlurmTime(limit)
	}

	j.DependsOn = parseSlurmDependencies(v["Dependency"], rec.cut["Dependency"])

	j.SubmitTime = parseSlurmTimestamp(v["SubmitTime"])
	// A waiting job's start and a running job's end are the scheduler's estimates, not what happened.
	if j.State != JobPending {
		j.StartTime = parseSlurmTimestamp(v["StartTime"])
	}
	if j.State != JobPending && j.State != JobRunning {
		j.EndTime = parseSlurmTimestamp(v["EndTime"])
	}
	if !rec.cut["STDOUT"] {
		j.Stdout = slurmPath(v["STDOUT"])
	}
	if !rec.cut["STDERR"] {
		j.Stderr = slurmPath(v["STDERR"])
	}
	if j.Stderr == "" {
		j.Stderr = j.Stdout
	}
	if ranks, err := strconv.Atoi(v["NumTasks"]); err == nil && ranks > 1 && ranks < 1<<31 {
		j.Ranks = ranks
		j.CpusPerRank, _ = strconv.Atoi(v["cpus-per-task"])
		// 0 says the user did not fix the ranks per node, so the scheduler may place them unevenly.
		if perNode, err := strconv.Atoi(v["NTPerNode"]); err == nil && perNode > 0 {
			j.RanksPerNode = perNode
		}
	}
	return j
}

// slurmJobStates maps a SLURM job state to the job state.
// A state not listed is JobUnknown.
var slurmJobStates = map[string]JobState{
	"PENDING": JobPending, "CONFIGURING": JobPending, "STAGE_IN": JobPending,
	"REQUEUED": JobPending, "REQUEUE_HOLD": JobPending, "RESV_DEL_HOLD": JobPending,
	"RUNNING": JobRunning, "COMPLETING": JobRunning, "STAGE_OUT": JobRunning,
	"RESIZING": JobRunning, "SIGNALING": JobRunning, "SUSPENDED": JobRunning, "STOPPED": JobRunning,
	"COMPLETED": JobSuccess,
	"FAILED":    JobFailed, "TIMEOUT": JobFailed, "OUT_OF_MEMORY": JobFailed, "NODE_FAIL": JobFailed,
	"BOOT_FAIL": JobFailed, "PREEMPTED": JobFailed, "DEADLINE": JobFailed, "SPECIAL_EXIT": JobFailed,
	"CANCELLED": JobCancelled, "REVOKED": JobCancelled,
}

// slurmJobState reads a State field. A cancelled job reads "CANCELLED by 1000", so only the
// first word counts.
func slurmJobState(state string) (JobState, bool) {
	word, _, _ := strings.Cut(strings.TrimSpace(state), " ")
	word = strings.ToUpper(word)
	mapped, ok := slurmJobStates[word]
	if !ok {
		return JobUnknown, false
	}
	return mapped, word == "SUSPENDED" || word == "STOPPED"
}

// parseSlurmTRES reads a tres-alloc field, such as "cpu=4,mem=400M,node=1,gres/gpu=1,gres/gpu:a100=1".
// A GPU with a type is counted under its type, and the untyped total is used only when no type is named.
func parseSlurmTRES(tres string) (cpus int, memMB int64, nodes int, gpus []JobGpu) {
	untyped := 0
	for _, entry := range strings.Split(tres, ",") {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch {
		case key == "cpu":
			cpus, _ = strconv.Atoi(value)
		case key == "mem":
			memMB, _ = utils.ParseMemoryMB(value)
		case key == "node":
			nodes, _ = strconv.Atoi(value)
		case key == "gres/gpu":
			untyped, _ = strconv.Atoi(value)
		case strings.HasPrefix(key, "gres/gpu:"):
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				gpus = append(gpus, JobGpu{Type: strings.TrimPrefix(key, "gres/gpu:"), Count: n})
			}
		}
	}
	if len(gpus) == 0 && untyped > 0 {
		gpus = []JobGpu{{Type: "gpu", Count: untyped}}
	}
	return cpus, memMB, nodes, gpus
}

// requestedGpus is the GPUs a job asked for, for a site whose tres-alloc does not count GPUs.
// A request per job is the total. A request per node is multiplied by the nodes of the job.
// A field that was cut is not trusted.
func requestedGpus(perJob string, jobCut bool, perNode string, nodeCut bool, nodes int) []JobGpu {
	if gpus := parseSlurmGresRequest(perJob); len(gpus) > 0 && !jobCut {
		return gpus
	}
	if nodeCut {
		return nil
	}
	gpus := parseSlurmGresRequest(perNode)
	for i := range gpus {
		gpus[i].Count *= max(nodes, 1)
	}
	return gpus
}

// parseSlurmGresRequest reads a tres-per-node or tres-per-job field, such as "gres/gpu:quadro:2",
// "gres/gpu:2" or "gres/gpu:a100:1,gres/gpu:t4:1". A GPU with no type is "gpu", and a count that is left out is 1.
// Other resources and "N/A" give nothing.
func parseSlurmGresRequest(field string) []JobGpu {
	var gpus []JobGpu
	for _, entry := range strings.Split(field, ",") {
		rest, found := strings.CutPrefix(strings.TrimSpace(entry), "gres/gpu")
		if !found {
			continue
		}
		gpu := JobGpu{Type: "gpu", Count: 1}
		rest = strings.TrimPrefix(rest, ":")
		if rest != "" {
			name, count, hasCount := strings.Cut(rest, ":")
			if n, err := strconv.Atoi(rest); err == nil {
				gpu.Count = n
			} else {
				gpu.Type = name
				if hasCount {
					n, err := strconv.Atoi(count)
					if err != nil {
						continue
					}
					gpu.Count = n
				}
			}
		}
		if gpu.Count > 0 {
			gpus = append(gpus, gpu)
		}
	}
	return gpus
}

// parseSlurmDependencies reads a squeue Dependency field: entries such as
// "afterok:245070(unfulfilled)" joined by "," (all) or "?" (any), and "singleton(unfulfilled)".
// A "(failed)" entry can never hold. An entry with no state has held. When the field was cut, its
// last entry is partial and dropped.
func parseSlurmDependencies(field string, cut bool) []JobDependency {
	if field == "" || field == "(null)" {
		return nil
	}
	entries := strings.FieldsFunc(field, func(r rune) bool { return r == ',' || r == '?' })
	if cut && len(entries) > 0 {
		entries = entries[:len(entries)-1]
	}
	var deps []JobDependency
	for _, entry := range entries {
		dep := JobDependency{Satisfied: true}
		if open := strings.LastIndex(entry, "("); open >= 0 && strings.HasSuffix(entry, ")") {
			switch entry[open+1 : len(entry)-1] {
			case "unfulfilled":
				dep.Satisfied = false
			case "failed":
				dep.Satisfied, dep.Failed = false, true
			}
			entry = entry[:open]
		}
		dep.Type, dep.JobID, _ = strings.Cut(entry, ":")
		deps = append(deps, dep)
	}
	return deps
}

// parseSlurmTimestamp reads a squeue time in the standard format, "2026-11-12T14:52:25", as local time.
// "N/A", "Unknown" and anything unreadable give the zero time.
func parseSlurmTimestamp(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", strings.TrimSpace(s), time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// slurmPath is a log path squeue printed. It reads "(null)" for a file that is not set.
func slurmPath(s string) string {
	if s == "(null)" || s == "N/A" {
		return ""
	}
	return s
}

// expandSlurmNodeList turns a node list such as "c[161-162],gpu3" into node names.
// A bracket holds numbers and ranges, and a range keeps its zero padding. A list that does not
// parse is returned as it was written.
func expandSlurmNodeList(list string) []string {
	var names []string
	for _, entry := range splitSlurmNodeList(list) {
		if entry == "(null)" || entry == "N/A" {
			continue
		}
		names = append(names, expandHostEntry(entry)...)
	}
	return names
}

// expandHostEntry expands the first bracket group of entry, then the rest of it.
func expandHostEntry(entry string) []string {
	open := strings.Index(entry, "[")
	closeAt := strings.Index(entry, "]")
	if open < 0 || closeAt < open {
		return []string{entry}
	}
	prefix, suffix := entry[:open], entry[closeAt+1:]
	var out []string
	for _, part := range strings.Split(entry[open+1:closeAt], ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			hi = lo
		}
		start, err1 := strconv.Atoi(lo)
		end, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || end < start || end-start > 100000 {
			return []string{entry}
		}
		for n := start; n <= end; n++ {
			out = append(out, expandHostEntry(prefix+fmt.Sprintf("%0*d", len(lo), n)+suffix)...)
		}
	}
	return out
}
