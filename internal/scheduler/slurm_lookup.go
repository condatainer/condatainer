package scheduler

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// slurmFailures is why a job that SLURM reports as failed failed.
var slurmFailures = map[string]string{
	"FAILED": FailureNonzeroExit, "SPECIAL_EXIT": FailureNonzeroExit,
	"TIMEOUT": FailureTimeout, "OUT_OF_MEMORY": FailureOutOfMemory,
	"NODE_FAIL": FailureNodeFailure, "BOOT_FAIL": FailureNodeFailure,
	"PREEMPTED": FailurePreempted, "DEADLINE": FailureDeadline,
}

// squeueLookupFields are the fields of a lookup: the full listing, and the exit code of a job that ended.
var squeueLookupFields = append(append(append([]squeueField{}, squeueListFields...), squeueFullFields...),
	squeueField{"exit_code", 12})

// sacctFields are the columns of the sacct call. The job name is last, since it may hold the delimiter and the
// reader joins what is left after the other columns.
var sacctFields = []string{
	"JobID", "State", "ExitCode", "Submit", "Start", "End", "Elapsed", "Timelimit", "Partition", "NodeList",
	"AllocTRES", "ReqTRES", "TotalCPU", "MaxRSS", "StdOut", "StdErr", "JobName",
}

// ended reports whether a job's state is final.
func ended(s JobState) bool { return s == JobSuccess || s == JobFailed || s == JobCancelled }

// LookupJobs finds the jobs the IDs name with at most two calls.
//   - One squeue -t all answers for every job the controller still holds, whatever its state.
//   - One sacct answers for the IDs squeue did not return, for those it returned as ended, which
//     squeue has no CPU time for, and for arrays, whose tasks that left the queue only it lists.
func (s *SlurmScheduler) LookupJobs(ctx context.Context, ids []string) ([]JobInfo, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	squeueBin := siblingBin(s.sbatchBin, "squeue")
	if squeueBin == "" {
		return nil, NewClusterError("SLURM", "look up jobs", fmt.Errorf("squeue not found"))
	}
	args := []string{"--me", "-r", "-h", "-t", "all", "--expand-patterns", "-j", strings.Join(ids, ","),
		"-O", squeueFormat(squeueLookupFields)}
	out, err := runCommandEnv(ctx, slurmToolEnv("SQUEUE_", true), "SLURM", "look-up-jobs", squeueBin, args...)
	if err != nil {
		if _, ok := err.(*TimeoutError); ok {
			return nil, err
		}
		// One ID the controller does not know is an error, and says nothing about the others.
		if !strings.Contains(string(out), "Invalid job id specified") {
			return nil, NewClusterError("SLURM", "look up jobs", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
		}
		out = nil
	}
	queued, err := parseSqueueJobs(string(out), squeueLookupFields)
	if err != nil {
		return nil, err
	}

	var history []JobInfo
	var historyErr error
	if need := idsNeedingHistory(ids, queued); len(need) > 0 {
		history, historyErr = s.sacctJobs(ctx, need)
		if _, ok := historyErr.(*TimeoutError); ok {
			return nil, historyErr
		}
	}
	return mergeLookup(ids, queued, history, historyErr), nil
}

// idsNeedingHistory is the requested IDs that squeue did not answer completely: those it did not return,
// those it returned as ended, and arrays, whose earlier tasks may have left the queue.
func idsNeedingHistory(ids []string, queued []JobInfo) []string {
	var need []string
	for _, id := range ids {
		rows := rowsOf(id, queued)
		if len(rows) == 0 {
			need = append(need, id)
			continue
		}
		for _, r := range rows {
			if ended(r.State) || (r.IsTask() && r.ID != id) {
				need = append(need, id)
				break
			}
		}
	}
	return need
}

// rowsOf is the jobs an ID names: the job itself, or the tasks of an array of that ID.
func rowsOf(id string, jobs []JobInfo) []JobInfo {
	var rows []JobInfo
	for _, j := range jobs {
		if j.ID == id || j.ArrayID == id {
			rows = append(rows, j)
		}
	}
	return rows
}

// mergeLookup answers each requested ID, in order, from squeue's rows and sacct's.
//   - squeue's row is kept for a job it holds. sacct adds the usage of one that ended.
//   - A job only sacct knows comes from sacct.
//   - An ID nobody knows is not found. When sacct failed it is unknown, except where accounting is off.
func mergeLookup(ids []string, queued, history []JobInfo, historyErr error) []JobInfo {
	all := append([]JobInfo{}, queued...)
	at := map[string]int{}
	for i, j := range all {
		at[j.ID] = i
	}
	for _, h := range history {
		i, held := at[h.ID]
		switch {
		case !held:
			at[h.ID] = len(all)
			all = append(all, h)
		case ended(all[i].State):
			all[i].CpuTime, all[i].PeakMemMB, all[i].Source = h.CpuTime, h.PeakMemMB, SourceHistory
		}
	}

	var out []JobInfo
	seen := map[string]bool{}
	for _, id := range ids {
		rows := rowsOf(id, all)
		if len(rows) == 0 {
			rows = []JobInfo{{ID: id, State: lookupMiss(historyErr)}}
		}
		for _, r := range rows {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// lookupMiss is the state of an ID no source returned. A failed sacct cannot say it never existed,
// unless the site keeps no accounting at all.
func lookupMiss(historyErr error) JobState {
	if historyErr != nil && !strings.Contains(historyErr.Error(), "accounting storage is disabled") {
		return JobUnknown
	}
	return JobNotFound
}

// sacctJobs reads the jobs named by ids from the accounting records with one sacct call.
func (s *SlurmScheduler) sacctJobs(ctx context.Context, ids []string) ([]JobInfo, error) {
	sacctBin := siblingBin(s.sbatchBin, "sacct")
	if sacctBin == "" {
		return nil, NewClusterError("SLURM", "look up job history", fmt.Errorf("sacct not found"))
	}
	args := []string{"--array", "-n", "-P", "-j", strings.Join(ids, ","), "-o", strings.Join(sacctFields, ",")}
	out, err := runCommandEnv(ctx, slurmToolEnv("SACCT_", true), "SLURM", "look-up-history", sacctBin, args...)
	if err != nil {
		if _, ok := err.(*TimeoutError); ok {
			return nil, err
		}
		return nil, NewClusterError("SLURM", "look up job history", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
	}
	return parseSacctJobs(string(out)), nil
}

var slurmTaskIDRe = regexp.MustCompile(`^([0-9]+)_([0-9]+)$`)

// parseSacctJobs reads sacct -P rows of sacctFields into one JobInfo per job.
//   - A job row has an ID with no ".". Its step rows follow it, and the highest MaxRSS of them is the peak memory.
//   - A row for tasks not yet run, such as "9_[4-9%4]", is not a job and is skipped.
func parseSacctJobs(output string) []JobInfo {
	var jobs []JobInfo
	at := map[string]int{}
	peak := map[string]int64{}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.SplitN(line, "|", len(sacctFields))
		if len(cols) < len(sacctFields) {
			continue
		}
		row := map[string]string{}
		for i, name := range sacctFields {
			row[name] = cols[i]
		}
		id := row["JobID"]
		if strings.ContainsAny(id, "[+") {
			continue
		}
		if job, _, isStep := strings.Cut(id, "."); isStep {
			if mb, err := utils.ParseMemoryMB(row["MaxRSS"]); err == nil && mb > peak[job] {
				peak[job] = mb
			}
			continue
		}
		at[id] = len(jobs)
		jobs = append(jobs, jobFromSacct(row))
	}
	for id, mb := range peak {
		if i, ok := at[id]; ok {
			jobs[i].PeakMemMB = mb
		}
	}
	return jobs
}

// jobFromSacct builds a JobInfo from the job row of a sacct call.
func jobFromSacct(row map[string]string) JobInfo {
	j := JobInfo{ID: row["JobID"], Name: row["JobName"], Source: SourceHistory}
	if m := slurmTaskIDRe.FindStringSubmatch(j.ID); m != nil {
		j.ArrayID, j.TaskID = m[1], m[2]
	}
	state := row["State"]
	j.State, j.Suspended = slurmJobState(state)
	setEnd(&j, state, row["ExitCode"])

	for _, p := range strings.Split(row["Partition"], ",") {
		if p != "" {
			j.Partitions = append(j.Partitions, p)
		}
	}
	if nodes := row["NodeList"]; nodes != "None assigned" {
		j.NodeList = expandSlurmNodeList(nodes)
	}
	tres := row["AllocTRES"]
	if tres == "" {
		tres = row["ReqTRES"]
	}
	j.Cpus, j.MemMB, j.NodeCount, j.Gpus = parseSlurmTRES(tres)
	if j.NodeCount == 0 {
		j.NodeCount = len(j.NodeList)
	}

	j.SubmitTime = parseSlurmTimestamp(row["Submit"])
	j.StartTime = parseSlurmTimestamp(row["Start"])
	j.EndTime = parseSlurmTimestamp(row["End"])
	j.Elapsed = parseSlurmTime(row["Elapsed"])
	switch limit := row["Timelimit"]; limit {
	case "UNLIMITED":
		j.TimeUnlimited = true
	case "", "Partition_Limit", "NOT_SET", "INVALID":
	default:
		j.TimeLimit = parseSlurmTime(limit)
	}
	if !j.StartTime.IsZero() {
		cpu := parseSlurmCPUTime(row["TotalCPU"])
		j.CpuTime = &cpu
	}
	j.Stdout, j.Stderr = slurmPath(row["StdOut"]), slurmPath(row["StdErr"])
	if j.Stderr == "" {
		j.Stderr = j.Stdout
	}
	return j
}

// setEnd fills Failure, ExitCode and ExitSignal of an ended job from its SLURM state and "code:signal".
// Only a job whose own program ended it has an exit code. When SLURM ended it, the code is SLURM's kill.
func setEnd(j *JobInfo, state, exit string) {
	word, _, _ := strings.Cut(strings.TrimSpace(state), " ")
	word = strings.ToUpper(word)
	if j.State == JobFailed {
		j.Failure = slurmFailures[word]
	}
	if j.State != JobSuccess && j.Failure != FailureNonzeroExit {
		return
	}
	code, signal, found := strings.Cut(exit, ":")
	c, err := strconv.Atoi(code)
	if !found || err != nil {
		return
	}
	j.ExitCode = &c
	if sig, err := strconv.Atoi(signal); err == nil && sig != 0 {
		j.ExitSignal = &sig
	}
}

// parseSlurmCPUTime reads a TotalCPU such as "31:43.306", "02:57:00" or "1-02:03:04.500". An unreadable one is 0.
func parseSlurmCPUTime(s string) time.Duration {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	d := parseSlurmTime(whole)
	if frac != "" {
		if f, err := strconv.ParseFloat("0."+frac, 64); err == nil {
			d += time.Duration(f * float64(time.Second))
		}
	}
	return d
}
