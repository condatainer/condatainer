package scheduler

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"github.com/condatainer/condatainer/internal/utils"
)

// logParseWarning logs a parser diagnostic warning, except in a job.
func logParseWarning(format string, args ...any) {
	if !IsInsideJob() {
		slog.Default().Warn(fmt.Sprintf(format, args...))
	}
}

// readFileLines opens a file and returns all its lines.
// Shared helper used by all scheduler ReadScriptSpecs implementations.
func readFileLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrScriptNotFound, path)
		}
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading script: %w", err)
	}
	return lines, nil
}

// parseScript is the two-stage pipeline behind each scheduler's ReadScriptSpecs.
//   - Stage 1, ParseRuntimeConfig (job control), is critical. An error stops it, and its fields never reach RemainingFlags.
//   - Stage 2, parseResourceSpec (geometry), is best effort. A failure returns nil, and the script becomes passthrough with a warning.
//   - Unconsumed directives go to RemainingFlags. RawFlags keeps every directive.
func parseScript(
	scriptPath string,
	lines []string,
	extractor func([]string) []string,
	rcParser func([]string) (RuntimeConfig, []string, error),
	rsParser func([]string) (*ResourceSpec, []string),
) (*ScriptSpecs, error) {
	directives := extractor(lines)

	rc, unconsumed, err := rcParser(directives)
	if err != nil {
		return nil, err // Critical: RuntimeConfig parse failure stops everything
	}

	rs, remaining := rsParser(unconsumed)
	if rs == nil && len(unconsumed) > 0 {
		// Duplicate warning, already printed by the scheduler-specific parser; no need to print again here.
		// utils.PrintWarning("Could not parse resource directives; using passthrough mode")
	}

	return &ScriptSpecs{
		ScriptPath:     scriptPath,
		Spec:           rs,
		Control:        rc,
		HasDirectives:  len(directives) > 0,
		RawFlags:       directives,
		RemainingFlags: remaining,
	}, nil
}

// flagValue extracts the value from a CLI flag, trying each prefix in order.
//   - Handles "prefix=value" and "prefix value" (space-separated) forms.
//   - Returns ("", false) if no prefix matches.
func flagValue(flag string, prefixes ...string) (string, bool) {
	for _, prefix := range prefixes {
		if v, ok := strings.CutPrefix(flag, prefix+"="); ok {
			return v, true
		}
		if v, ok := strings.CutPrefix(flag, prefix+" "); ok {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// flagMatches reports whether flag matches any prefix in "prefix=…" or "prefix …" form.
// Intended for use in switch case expressions.
func flagMatches(flag string, prefixes ...string) bool {
	_, ok := flagValue(flag, prefixes...)
	return ok
}

// flagScan extracts a value from a CLI flag and writes it into *dest using the provided parser.
//   - Generic base — works for any type (int, int64, time.Duration, *GpuSpec, …).
//   - Returns (false, nil) when no prefix matches; (true, err) on parse failure.
func flagScan[T any](flag string, dest *T, parser func(string) (T, error), prefixes ...string) (bool, error) {
	v, ok := flagValue(flag, prefixes...)
	if !ok {
		return false, nil
	}
	result, err := parser(v)
	if err == nil {
		*dest = result
	}
	return true, err
}

// flagScanInt is a convenience wrapper for flagScan using strconv.Atoi.
func flagScanInt(flag string, dest *int, prefixes ...string) (bool, error) {
	return flagScan(flag, dest, strconv.Atoi, prefixes...)
}

// safeJobName converts a job name to a filesystem-safe string by replacing "/" with "--".
func safeJobName(name string) string {
	return strings.ReplaceAll(name, "/", "--")
}

// absPath returns the absolute form of path. If path is already absolute or
// filepath.Abs fails, it returns path unchanged.
func absPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// ResourceEnvVars returns the KEY=VALUE resource variables derived from rs.
//   - Always set: NNODES, NTASKS, NCPUS (cpus per task) and OMP_NUM_THREADS.
//   - NTASKS_PER_NODE only when TasksPerNode is known.
//   - MEM (MB per task) and MEM_GB when memory is specified.
//   - Parsing rounds TasksPerNode up when Nodes and Ntasks do not divide evenly. The script then enforces the layout memory assumed.
func ResourceEnvVars(rs *ResourceSpec) []string {
	nodes := 1
	cpusPerTask := 1
	var tasksPerNode int // 0 = unknown / free-distribution
	var directNtasks int // rs.Ntasks when explicitly set

	if rs != nil {
		if rs.Nodes > 0 {
			nodes = rs.Nodes
		}
		if rs.TasksPerNode > 0 {
			tasksPerNode = rs.TasksPerNode
		}
		if rs.CpusPerTask > 0 {
			cpusPerTask = rs.CpusPerTask
		}
		directNtasks = rs.Ntasks
	}

	// Total task count: prefer the directly set Ntasks; fall back to topology derivation.
	ntasks := directNtasks
	if ntasks <= 0 {
		t := tasksPerNode
		if t <= 0 {
			t = 1
		}
		ntasks = nodes * t
	}

	env := []string{
		fmt.Sprintf("NNODES=%d", nodes),
		fmt.Sprintf("NTASKS=%d", ntasks),
		fmt.Sprintf("NCPUS=%d", cpusPerTask),
		fmt.Sprintf("OMP_NUM_THREADS=%d", cpusPerTask),
	}
	if tasksPerNode > 0 {
		env = append(env,
			fmt.Sprintf("NTASKS_PER_NODE=%d", tasksPerNode),
		)
	}

	// Memory environment variables: MEM = memory per task
	if rs != nil {
		if memPerTaskMB := rs.GetMemPerTaskMB(); memPerTaskMB > 0 {
			env = append(env,
				fmt.Sprintf("MEM=%d", memPerTaskMB),
				fmt.Sprintf("MEM_GB=%d", memPerTaskMB/1024),
			)
		}
	}
	return env
}

// writeJobHeader writes the job info echo block.
//   - jobIDVar is the shell expression for the job ID.
//   - Resource lines print only when specs.Spec is set, and specs.ScriptPath when present.
//   - formatTime renders rs.Time. nil skips that line.
func writeJobHeader(w io.Writer, jobIDVar string, specs *ScriptSpecs, formatTime func(time.Duration) string, metadata map[string]string) {
	fmt.Fprintln(w, "# Print job information")
	fmt.Fprintln(w, "_START_TIME=$SECONDS")
	fmt.Fprintln(w, "_format_time() { local s=$1; printf '%02d:%02d:%02d' $((s/3600)) $((s%3600/60)) $((s%60)); }")
	fmt.Fprintln(w, "echo \"========================================\"")
	fmt.Fprintf(w, "echo \"Job ID:     %s\"\n", jobIDVar)
	jobName := ""
	if specs != nil {
		jobName = specs.Control.JobName
	}
	fmt.Fprintf(w, "echo \"Job Name:   %s\"\n", jobName)
	if specs != nil && specs.ScriptPath != "" {
		fmt.Fprintf(w, "echo \"Script:     %s\"\n", specs.ScriptPath)
	}

	// Extract ResourceSpec (may be nil → passthrough mode)
	rs := (*ResourceSpec)(nil)
	if specs != nil {
		rs = specs.Spec
	}
	if rs != nil {
		nodes := rs.Nodes
		if nodes <= 0 {
			nodes = 1
		}
		if nodes > 1 {
			fmt.Fprintf(w, "echo \"Nodes:      %d\"\n", nodes)
		}
		if rs.GetNtasks() > 1 {
			fmt.Fprintf(w, "echo \"Tasks:      %d\"\n", rs.GetNtasks())
		}
		if rs.TasksPerNode > 0 {
			fmt.Fprintf(w, "echo \"Tasks/Node: %d\"\n", rs.TasksPerNode)
		}
		if rs.CpusPerTask > 0 {
			fmt.Fprintf(w, "echo \"CPUs/Task:  %d\"\n", rs.CpusPerTask)
		}
		if memPerTaskMB := rs.GetMemPerTaskMB(); memPerTaskMB > 0 {
			fmt.Fprintf(w, "echo \"Mem/Task:   %d MB\"\n", memPerTaskMB)
		}
		if rs.Time > 0 && formatTime != nil {
			fmt.Fprintf(w, "echo \"Time:       %s\"\n", formatTime(rs.Time))
		}
	}
	fmt.Fprintln(w, "echo \"PWD:        $(pwd)\"")
	if len(metadata) > 0 {
		maxLen := 0
		for key := range metadata {
			if len(key) > maxLen {
				maxLen = len(key)
			}
		}
		for key, value := range metadata {
			if value != "" {
				padding := maxLen - len(key)
				fmt.Fprintf(w, "echo \"%s:%s %s\"\n", key, strings.Repeat(" ", padding+4), value)
			}
		}
	}
	fmt.Fprintf(w, "%s\n", "echo \"Started:    $(date '+%Y-%m-%d %T')\"")
	fmt.Fprintln(w, "echo \"========================================\"")
	// Set OMP_NUM_THREADS to the allocated CPUs per task if not already set by the scheduler.
	if rs != nil && rs.CpusPerTask > 0 {
		fmt.Fprintf(w, "export OMP_NUM_THREADS=${OMP_NUM_THREADS:-%d}\n", rs.CpusPerTask)
	}
	// Per-job proxy: start tunnel back to the submitting login node.
	if specs != nil && specs.ProxyVia != "" {
		fmt.Fprintf(w, "condatainer proxy start --via %s 2>/dev/null || true\n", specs.ProxyVia)
	}
}

// writeJobFooter writes the job completion footer echo block to w.
// jobIDVar is the shell expression for the job ID (e.g. "$SLURM_JOB_ID").
func writeJobFooter(w io.Writer, jobIDVar string) {
	fmt.Fprintln(w, "echo \"========================================\"")
	fmt.Fprintf(w, "echo \"Job ID:    %s\"\n", jobIDVar)
	fmt.Fprintln(w, "echo \"Elapsed:   $(_format_time $(($SECONDS - $_START_TIME)))\"")
	fmt.Fprintf(w, "%s\n", "echo \"Completed: $(date '+%Y-%m-%d %T')\"")
	fmt.Fprintln(w, "echo \"Exit Code: $_EXIT_CODE\"")
	fmt.Fprintln(w, "echo \"========================================\"")
}

// writeArrayBlock writes the block that extracts this task's line from inputFile into ARRAY_ARGS and redirects output to logDir.
//   - Call it after the scheduler directives and before writeJobHeader.
//   - taskIDVar is the 1-based task index expression, also the sed line number.
//   - Log names use jobName, the index padded to the width of count, and _ARRAY_TAG.
//   - _ARRAY_TAG is ARRAY_ARGS made filename-safe: non-alphanumerics become "_", runs squeeze, capped at 20 characters.
//   - Combined output goes to .log, or to .out and .err when separateOutput is set.
func writeArrayBlock(w io.Writer, taskIDVar, inputFile, logDir, jobName string,
	count int, separateOutput bool) {

	padWidth := len(fmt.Sprintf("%d", count))
	fmt.Fprintln(w, "# Array job: extract input and redirect output")
	fmt.Fprintf(w, "_ARRAY_IDX=%s\n", taskIDVar)
	fmt.Fprintf(w, "ARRAY_ARGS=$(sed -n \"${_ARRAY_IDX}p\" %s)\n", inputFile)
	fmt.Fprintln(w, "export ARRAY_ARGS")
	fmt.Fprintf(w, "_ARRAY_TAG=$(printf '%%s' \"$ARRAY_ARGS\" | tr -cs '[:alnum:]_-' '_' | cut -c1-20)\n")
	fmt.Fprintln(w, "_ARRAY_TAG=${_ARRAY_TAG%_}")
	fmt.Fprintf(w, "_PADDED_IDX=$(printf \"%%0%dd\" \"$_ARRAY_IDX\")\n", padWidth)

	if separateOutput {
		fmt.Fprintf(w,
			"exec > \"%s/%s_${_PADDED_IDX}_${_ARRAY_TAG}.out\""+
				" 2> \"%s/%s_${_PADDED_IDX}_${_ARRAY_TAG}.err\"\n",
			logDir, jobName, logDir, jobName)
	} else {
		fmt.Fprintf(w, "exec &> \"%s/%s_${_PADDED_IDX}_${_ARRAY_TAG}.log\"\n",
			logDir, jobName)
	}
	fmt.Fprintln(w)
}

// parseMemoryMB converts memory strings like "8G", "1024M", "512K" to MB.
// Delegates to utils.ParseMemoryMB; wraps error with ErrInvalidMemoryFormat sentinel.
func parseMemoryMB(memStr string) (int64, error) {
	mb, err := utils.ParseMemoryMB(memStr)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrInvalidMemoryFormat, memStr)
	}
	return mb, nil
}

// formatHMSTime formats a duration as "HH:MM:SS" (PBS / HTCondor walltime format).
func formatHMSTime(d time.Duration) string {
	total := int64(d.Seconds())
	hours := total / 3600
	mins := (total % 3600) / 60
	secs := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, mins, secs)
}
