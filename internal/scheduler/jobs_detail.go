package scheduler

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/condatainer/condatainer/internal/utils"
)

// maxListedTasks is how many task IDs one line of the detail block lists.
const maxListedTasks = 10

// formatHMS writes a duration as HH:MM:SS, hours not wrapping at 24.
func formatHMS(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s%3600/60, s%60)
}

// formatSizeMB writes a size in MB as "400 MB", "40 GB" or "1.5 GB".
func formatSizeMB(mb int64) string {
	if mb < 1024 {
		return strconv.FormatInt(mb, 10) + " MB"
	}
	gb := float64(mb) / 1024
	if gb >= 10 {
		return fmt.Sprintf("%.0f GB", gb)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", gb), ".0") + " GB"
}

// WriteJobDetail prints one job as a block: its ID, state and name, then a line for each fact the
// scheduler reported. A line's label is the JSON key of the fact, with "_" shown as a space and the
// unit suffix dropped. A fact that is not reported has no line.
func WriteJobDetail(w io.Writer, j JobInfo) {
	title := j.ID + "  " + string(j.State)
	if j.State == JobFailed {
		title = j.ID + "  " + utils.StyleError(string(j.State))
	}
	if j.Name != "" {
		title += "  " + j.Name
	}
	fmt.Fprintln(w, title)

	var lines [][2]string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, [2]string{label, value})
		}
	}
	timeOf := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Format("2006-01-02 15:04:05")
	}

	add("partitions", strings.Join(j.Partitions, ", "))
	add("failure", j.Failure)
	if j.ExitCode != nil {
		add("exit code", strconv.Itoa(*j.ExitCode))
	}
	if j.ExitSignal != nil {
		add("exit signal", strconv.Itoa(*j.ExitSignal))
	}
	if j.Suspended {
		add("suspended", "yes")
	}
	add("reason", j.Reason)
	add("depends on", dependencyText(j.DependsOn))
	add("resources", resourcesText(j))
	add("node list", strings.Join(j.NodeList, ", "))
	switch {
	case j.TimeUnlimited:
		add("time limit", "unlimited")
	case j.TimeLimit > 0:
		add("time limit", formatHMS(j.TimeLimit))
	}
	if !j.IsArray() && !j.StartTime.IsZero() {
		add("elapsed", formatHMS(j.Elapsed))
	}
	add("usage", usageText(j))
	if j.IsArray() {
		lines = append(lines, taskLines(j.Tasks)...)
	}
	add("submit time", timeOf(j.SubmitTime))
	add("start time", timeOf(j.StartTime))
	add("end time", timeOf(j.EndTime))
	if !j.StartTime.IsZero() && !j.SubmitTime.IsZero() {
		add("queued", formatHMS(j.StartTime.Sub(j.SubmitTime)))
	}
	add("stdout", j.Stdout)
	if j.Stderr != j.Stdout {
		add("stderr", j.Stderr)
	}

	width := 0
	for _, l := range lines {
		width = max(width, utf8.RuneCountInString(l[0]))
	}
	for _, l := range lines {
		fmt.Fprintf(w, "  %-*s  %s\n", width, l[0], l[1])
	}
}

// dependencyText is the parents of a job as "afterok:245070 unfulfilled".
func dependencyText(deps []JobDependency) string {
	parts := make([]string, len(deps))
	for i, d := range deps {
		text := d.Type
		if d.JobID != "" {
			text += ":" + d.JobID
		}
		switch {
		case d.Failed:
			text += " failed"
		case d.Satisfied:
			text += " met"
		default:
			text += " unfulfilled"
		}
		parts[i] = text
	}
	return strings.Join(parts, ", ")
}

// resourcesText is "4 cpus, 40 GB, 1 h100", with the layout of a job of several nodes or ranks after it.
func resourcesText(j JobInfo) string {
	var parts []string
	if j.Cpus > 0 {
		parts = append(parts, plural(j.Cpus, "cpu"))
	}
	if j.MemMB > 0 {
		parts = append(parts, formatSizeMB(j.MemMB))
	}
	for _, g := range j.Gpus {
		parts = append(parts, strconv.Itoa(g.Count)+" "+g.Type)
	}
	text := strings.Join(parts, ", ")
	if layout := layoutText(j); layout != "" && text != "" {
		text += " (" + layout + ")"
	}
	return text
}

// layoutText is the shape of a job of several ranks or nodes. The ranks per node are shown only when the
// user fixed them: "2 nodes x 4 ranks x 2 cpus". Otherwise it is "8 ranks x 2 cpus on 2 nodes".
func layoutText(j JobInfo) string {
	if j.Ranks <= 1 {
		if j.NodeCount > 1 {
			return plural(j.NodeCount, "node")
		}
		return ""
	}
	cpus := ""
	if j.CpusPerRank > 0 {
		cpus = " x " + plural(j.CpusPerRank, "cpu")
	}
	if uniformLayout(j) {
		return plural(j.NodeCount, "node") + " x " + plural(j.RanksPerNode, "rank") + cpus
	}
	text := plural(j.Ranks, "rank") + cpus
	if j.NodeCount > 1 {
		text += " on " + plural(j.NodeCount, "node")
	}
	return text
}

// uniformLayout reports whether every node of the job has the same number of ranks, because the user fixed it.
func uniformLayout(j JobInfo) bool {
	return j.RanksPerNode > 0 && j.NodeCount > 0 && j.Ranks == j.RanksPerNode*j.NodeCount
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// usageText is "cpu time 02:57:00, peak mem 11 GB", with what the scheduler did not report left out.
func usageText(j JobInfo) string {
	var parts []string
	if j.CpuTime != nil {
		parts = append(parts, "cpu time "+formatHMS(*j.CpuTime))
	}
	if j.PeakMemMB > 0 {
		parts = append(parts, "peak mem "+formatSizeMB(j.PeakMemMB))
	}
	return strings.Join(parts, ", ")
}

// taskLines are the lines of an array's tasks: the counts, then the IDs of the tasks in each state that
// is not all of them, up to maxListedTasks of each.
func taskLines(t *ArrayTasks) [][2]string {
	var counts []string
	groups := []struct {
		word string
		ids  []string
	}{{"success", t.Success}, {"failed", t.Failed}, {"cancelled", t.Cancelled}, {"running", t.Running},
		{"pending", t.Pending}, {"unknown", t.Other}}
	for _, g := range groups {
		if len(g.ids) > 0 {
			counts = append(counts, strconv.Itoa(len(g.ids))+" "+g.word)
		}
	}
	lines := [][2]string{{"tasks", strconv.Itoa(t.Total()) + ": " + strings.Join(counts, ", ")}}
	for _, g := range groups {
		if g.word == "success" || len(g.ids) == 0 {
			continue
		}
		list := g.ids
		text := ""
		if len(list) > maxListedTasks {
			text = fmt.Sprintf(" (+%d more)", len(list)-maxListedTasks)
			list = list[:maxListedTasks]
		}
		lines = append(lines, [2]string{g.word, strings.Join(list, ", ") + text})
	}
	return lines
}
