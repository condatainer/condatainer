package scheduler

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/condatainer/condatainer/internal/utils"
)

// formatMem writes a per-node memory figure as "8192M", "2000G" or "12T".
// The unit changes only from 10240 of the unit below: 10240M is "10G" and 10240G is "10T".
func formatMem(mb int64) string {
	switch {
	case mb >= 10240*1024:
		return strings.TrimSuffix(strconv.FormatFloat(float64(mb)/(1024*1024), 'f', 1, 64), ".0") + "T"
	case mb >= 10240:
		return fmt.Sprintf("%.0fG", float64(mb)/1024)
	default:
		return fmt.Sprintf("%dM", mb)
	}
}

// formatHM writes a duration as HH:MM, hours not wrapping at 24: three days is "72:00".
func formatHM(d time.Duration) string {
	minutes := int(d.Minutes())
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// partitionColumns are the columns of the partition table. Numbers align right.
var partitionColumns = []string{"PARTITION", "AVAIL", "NODES", "CPUS", "MEM", "MAXTIME", "MAXNODES", "GPUS"}

var partitionRightAligned = map[int]bool{2: true, 3: true, 4: true, 5: true, 6: true}

// maxNodesColumn is the index of MAXNODES in partitionColumns.
const maxNodesColumn = 6

// PartitionNodeTypes is the node types of a partition. A scheduler that reports none has one,
// built from the partition's largest host and the GPU types of the partition.
func PartitionNodeTypes(l ResourceLimits, fallbackGpus []GpuInfo) []NodeType {
	if len(l.NodeTypes) > 0 {
		return l.NodeTypes
	}
	t := NodeType{
		Nodes: l.Nodes, Cpus: l.MaxCpusPerNode, MemMB: l.MaxMemMBPerNode,
		CpusLowerBound: l.CpusLowerBound, MemLowerBound: l.MemLowerBound,
	}
	for _, g := range fallbackGpus {
		t.Gpus = append(t.Gpus, NodeGpu{Type: g.Type})
	}
	return []NodeType{t}
}

// gpuKey names a GPU set, so node types with the same GPUs compare equal.
func gpuKey(gpus []NodeGpu) string {
	parts := make([]string, len(gpus))
	for i, g := range gpus {
		parts[i] = g.Type + ":" + strconv.Itoa(g.PerNode)
	}
	return strings.Join(parts, ",")
}

// sameMemoryClass reports whether two node memory sizes differ by at most 10%.
// An unknown size (0) joins any class.
func sameMemoryClass(a, b int64) bool {
	if a == 0 || b == 0 {
		return true
	}
	return max(a, b)*10 <= min(a, b)*11
}

// addNodesUp adds two counts of nodes that are up. The sum is unreported when both are.
func addNodesUp(a, b *int) *int {
	if a == nil && b == nil {
		return nil
	}
	sum := 0
	if a != nil {
		sum += *a
	}
	if b != nil {
		sum += *b
	}
	return &sum
}

// mergeMemoryClasses joins node types with the same CPUs and GPUs whose memory is within 10% of the
// smallest of the joined type. The joined type takes that smallest size, marked as a lower bound
// when the sizes differ. Types keep the order of their first appearance.
func mergeMemoryClasses(types []NodeType) []NodeType {
	var merged []NodeType
	for _, t := range types {
		j := -1
		for i := range merged {
			m := merged[i]
			if m.Cpus == t.Cpus && gpuKey(m.Gpus) == gpuKey(t.Gpus) && sameMemoryClass(m.MemMB, t.MemMB) {
				j = i
				break
			}
		}
		if j < 0 {
			merged = append(merged, t)
			continue
		}
		m := &merged[j]
		m.Nodes += t.Nodes
		m.NodesUp = addNodesUp(m.NodesUp, t.NodesUp)
		m.CpusLowerBound = m.CpusLowerBound || t.CpusLowerBound
		m.MemLowerBound = m.MemLowerBound || t.MemLowerBound
		switch {
		case m.MemMB == 0:
			m.MemMB = t.MemMB
		case t.MemMB != 0 && t.MemMB != m.MemMB:
			m.MemLowerBound = true
			m.MemMB = min(m.MemMB, t.MemMB)
		}
	}
	return merged
}

// partitionRows turns one partition into table rows, one per node type and GPU line.
// Node types whose memory is within 10% share a row.
// The partition's name, state, time and node cap are on its first row only. The cap is "-" unless the scheduler reports one.
// fallbackGpus are the GPU types of a partition whose scheduler reports no node types.
func partitionRows(l ResourceLimits, fallbackGpus []GpuInfo) [][]string {
	types := mergeMemoryClasses(PartitionNodeTypes(l, fallbackGpus))

	name := PartitionName(l.Partition)
	if l.Default {
		name += "*"
	}
	avail := "-"
	if l.State != "" {
		avail = l.State
	}
	maxTime := "-"
	switch {
	case l.TimeUnlimited:
		maxTime = "inf"
	case l.MaxTime > 0:
		maxTime = formatHM(l.MaxTime)
	}
	maxNodes := "-"
	if l.MaxNodes > 0 {
		maxNodes = strconv.Itoa(l.MaxNodes)
	}

	var rows [][]string
	for ti, t := range types {
		nodes, cpus, mem := "-", "-", "-"
		if t.Nodes > 0 {
			nodes = strconv.Itoa(t.Nodes)
		}
		if t.Cpus > 0 {
			cpus = strconv.Itoa(t.Cpus)
			if t.CpusLowerBound {
				cpus += "+"
			}
		}
		if t.MemMB > 0 {
			mem = formatMem(t.MemMB)
			if t.MemLowerBound {
				mem += "+"
			}
		}

		gpuLines := make([]string, 0, len(t.Gpus))
		for _, g := range t.Gpus {
			if g.PerNode > 0 {
				gpuLines = append(gpuLines, g.Type+":"+strconv.Itoa(g.PerNode))
			} else {
				gpuLines = append(gpuLines, g.Type)
			}
		}
		if len(gpuLines) == 0 {
			gpuLines = []string{"-"}
		}

		for gi, gpu := range gpuLines {
			row := make([]string, len(partitionColumns))
			if ti == 0 && gi == 0 {
				row[0], row[1], row[5], row[6] = name, avail, maxTime, maxNodes
			}
			if gi == 0 {
				row[2], row[3], row[4] = nodes, cpus, mem
			}
			row[7] = gpu
			rows = append(rows, row)
		}
	}
	return rows
}

// writeTable prints a header and rows in the CLI table style: upper-case one-word headers, two spaces
// between columns, no rule under the header. Text aligns left and the columns in right align right.
// style, keyed by row and column, colours a cell after it is padded, so widths ignore the colour codes.
func writeTable(w io.Writer, header []string, rows [][]string, right map[int]bool, style map[[2]int]func(string) string) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}

	line := func(cells []string, r int) {
		var b strings.Builder
		for i, cell := range cells {
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell))
			text := cell
			if f, ok := style[[2]int{r, i}]; ok && cell != "" {
				text = f(cell)
			}
			if i > 0 {
				b.WriteString("  ")
			}
			switch {
			case right[i]:
				b.WriteString(pad)
				b.WriteString(text)
			case i == len(cells)-1:
				b.WriteString(text)
			default:
				b.WriteString(text)
				b.WriteString(pad)
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}

	line(header, -1)
	for r, row := range rows {
		line(row, r)
	}
}

// PartitionName is the name a partition or a GPU is listed under. One with no name is "default".
func PartitionName(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

// WritePartitionTable prints the partitions as one table, a row per node type.
// gpusByPartition holds the GPU types of a partition whose scheduler reports no node types.
// A partition that is not up has its state in red.
func WritePartitionTable(w io.Writer, limits []ResourceLimits, gpusByPartition map[string][]GpuInfo) {
	var rows [][]string
	style := map[[2]int]func(string) string{}
	capped := false
	for _, l := range limits {
		if !l.IsUp() {
			style[[2]int{len(rows), 1}] = utils.StyleError
		}
		capped = capped || l.MaxNodes > 0
		rows = append(rows, partitionRows(l, gpusByPartition[PartitionName(l.Partition)])...)
	}

	header, right := partitionColumns, partitionRightAligned
	if !capped {
		// No partition limits the nodes of a job, so the column says nothing.
		header = append(append([]string{}, header[:maxNodesColumn]...), header[maxNodesColumn+1:]...)
		right = make(map[int]bool)
		for i := range partitionColumns {
			switch {
			case i < maxNodesColumn:
				right[i] = partitionRightAligned[i]
			case i > maxNodesColumn:
				right[i-1] = partitionRightAligned[i]
			}
		}
		for i, row := range rows {
			rows[i] = append(append([]string{}, row[:maxNodesColumn]...), row[maxNodesColumn+1:]...)
		}
	}
	writeTable(w, header, rows, right, style)
}

// maxNodeCell is the widest a node list is shown. A longer list shows as a count.
const maxNodeCell = 26

// minFlexWidth is the narrowest the NAME and NOTE columns are cut to.
const minFlexWidth = 8

// jobColumn is a column of the jobs table. A flex column is cut with "…" when the table does not fit.
type jobColumn struct {
	header string
	right  bool
	flex   bool
	cell   func(JobInfo) string
}

var jobColumns = []jobColumn{
	{header: "JOBID", cell: func(j JobInfo) string { return j.ID }},
	{header: "NAME", flex: true, cell: func(j JobInfo) string { return j.Name }},
	{header: "STATE", cell: func(j JobInfo) string { return string(j.State) }},
	{header: "CPUS", right: true, cell: func(j JobInfo) string { return orDash(j.Cpus, strconv.Itoa(j.Cpus)) }},
	{header: "GPUS", cell: jobGpus},
	{header: "MEM", right: true, cell: func(j JobInfo) string { return orDash(int(j.MemMB), formatMem(j.MemMB)) }},
	{header: "TIME", cell: jobTime},
	{header: "NODE", cell: jobNode},
	{header: "NOTE", flex: true, cell: jobNote},
}

// orDash is text when n is positive, and "-" when the scheduler reported none.
func orDash(n int, text string) string {
	if n <= 0 {
		return "-"
	}
	return text
}

// jobGpus is the GPU types of a job as type:count, or "-".
func jobGpus(j JobInfo) string {
	parts := make([]string, len(j.Gpus))
	for i, g := range j.Gpus {
		parts[i] = g.Type + ":" + strconv.Itoa(g.Count)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// jobTime is the limit of a job that waits, elapsed over limit for one that runs, and elapsed for one that ended.
// An array shows its per-task limit only.
func jobTime(j JobInfo) string {
	limit := "-"
	switch {
	case j.TimeUnlimited:
		limit = "inf"
	case j.TimeLimit > 0:
		limit = formatHM(j.TimeLimit)
	}
	switch {
	case j.IsArray():
		return limit
	case j.State == JobRunning:
		return formatHM(j.Elapsed) + "/" + limit
	case ended(j.State) && j.Elapsed > 0:
		return formatHM(j.Elapsed)
	case ended(j.State):
		return "-"
	}
	return limit
}

// jobNode is the node a job runs on, cut at the first dot. A list shows when it fits, else a count.
// It is "-" for a job that never had one, and for an array.
func jobNode(j JobInfo) string {
	if j.IsArray() || len(j.NodeList) == 0 && j.NodeCount == 0 {
		return "-"
	}
	names := make([]string, len(j.NodeList))
	for i, n := range j.NodeList {
		names[i], _, _ = strings.Cut(n, ".")
	}
	list := strings.Join(names, ",")
	switch {
	case len(names) == 0 && j.NodeCount > 1:
		return strconv.Itoa(j.NodeCount) + " nodes"
	case len(names) == 0:
		return "-"
	case len(names) == 1:
		return cutText(list, maxNodeCell)
	case utf8.RuneCountInString(list) <= maxNodeCell:
		return list
	}
	return strconv.Itoa(len(names)) + " nodes"
}

// jobNote is the layout of a job of several ranks, then the item that explains the row.
func jobNote(j JobInfo) string {
	layout, note := compactLayout(j), jobReason(j)
	if layout != "" && note != "" {
		return layout + ", " + note
	}
	return layout + note
}

// compactLayout is "2n x 4r x 2c" for a job whose ranks per node the user fixed, and "8r x 2c" for one
// whose ranks the scheduler places. A job of one rank, and an array, have none.
func compactLayout(j JobInfo) string {
	if j.IsArray() || j.Ranks <= 1 {
		return ""
	}
	cpus := ""
	if j.CpusPerRank > 0 {
		cpus = " x " + strconv.Itoa(j.CpusPerRank) + "c"
	}
	if uniformLayout(j) {
		return strconv.Itoa(j.NodeCount) + "n x " + strconv.Itoa(j.RanksPerNode) + "r" + cpus
	}
	return strconv.Itoa(j.Ranks) + "r" + cpus
}

// jobReason is the one item that explains a row: the task counts of an array, that the job is suspended,
// a parent that failed, a dependency that is not met, the scheduler's reason for a job that waits,
// or why a job failed.
func jobReason(j JobInfo) string {
	if j.IsArray() {
		t := j.Tasks
		var parts []string
		for _, c := range []struct {
			n    int
			word string
		}{{len(t.Running), "running"}, {len(t.Pending), "pending"}, {len(t.Success), "success"},
			{len(t.Failed), "failed"}, {len(t.Cancelled), "cancelled"}, {len(t.Other), "unknown"}} {
			if c.n > 0 {
				parts = append(parts, strconv.Itoa(c.n)+" "+c.word)
			}
		}
		return strings.Join(parts, ", ")
	}
	if j.Suspended {
		return "suspended"
	}
	if j.State == JobPending || j.State == JobCancelled {
		for _, d := range j.DependsOn {
			if d.Failed {
				return "dep failed " + d.JobID
			}
		}
	}
	switch j.State {
	case JobFailed:
		if j.Failure == FailureNonzeroExit && j.ExitCode != nil {
			return "exit " + strconv.Itoa(*j.ExitCode)
		}
		return j.Failure
	case JobPending:
		var open []JobDependency
		for _, d := range j.DependsOn {
			if !d.Satisfied {
				open = append(open, d)
			}
		}
		if len(open) > 0 {
			note := dependencyWord(open[0].Type)
			if open[0].JobID != "" {
				note += " " + open[0].JobID
			}
			if len(open) > 1 {
				note += " +" + strconv.Itoa(len(open)-1)
			}
			return note
		}
		return j.Reason
	}
	return ""
}

// dependencyWord is how a dependency rule reads in a note. A job that needs its parent to succeed
// "waits after" it, and the other rules keep their own words.
func dependencyWord(t string) string {
	if t == DependencyAfterOK {
		return "after"
	}
	return t
}

// cutText cuts s to width characters, ending in "…" when it was longer.
func cutText(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:width-1]) + "…"
}

// WriteJobTable prints jobs as one table, a row each, in the order given.
//   - GPUS shows only when a job has a GPU.
//   - The table fits width. NAME and NOTE share what the other columns leave, and a longer value is cut.
//   - A failed job's state is in red.
func WriteJobTable(w io.Writer, jobs []JobInfo, width int) {
	cols := make([]jobColumn, 0, len(jobColumns))
	for _, c := range jobColumns {
		if c.header == "GPUS" && !slicesAnyGpu(jobs) {
			continue
		}
		cols = append(cols, c)
	}

	header := make([]string, len(cols))
	right := map[int]bool{}
	for i, c := range cols {
		header[i] = c.header
		right[i] = c.right
	}
	rows := make([][]string, len(jobs))
	style := map[[2]int]func(string) string{}
	for r, j := range jobs {
		rows[r] = make([]string, len(cols))
		for i, c := range cols {
			rows[r][i] = c.cell(j)
			if c.header == "STATE" && j.State == JobFailed {
				style[[2]int{r, i}] = utils.StyleError
			}
		}
	}

	flex := fitFlexColumns(header, rows, cols, width)
	for r := range rows {
		for i, limit := range flex {
			rows[r][i] = cutText(rows[r][i], limit)
		}
	}
	writeTable(w, header, rows, right, style)
}

func slicesAnyGpu(jobs []JobInfo) bool {
	for _, j := range jobs {
		if len(j.Gpus) > 0 {
			return true
		}
	}
	return false
}

// fitFlexColumns returns the width each flex column is cut to, keyed by column, so the table fits width.
// A flex column that needs no more than its share keeps its width, and the other takes the rest.
// No column is cut below minFlexWidth, so a very narrow width is exceeded.
func fitFlexColumns(header []string, rows [][]string, cols []jobColumn, width int) map[int]int {
	need := make([]int, len(cols))
	for i := range cols {
		need[i] = utf8.RuneCountInString(header[i])
		for _, row := range rows {
			need[i] = max(need[i], utf8.RuneCountInString(row[i]))
		}
	}
	fixed, flex := 2*(len(cols)-1), []int{}
	for i, c := range cols {
		if c.flex {
			flex = append(flex, i)
		} else {
			fixed += need[i]
		}
	}
	room := width - fixed
	limits := map[int]int{}
	total := 0
	for _, i := range flex {
		total += need[i]
	}
	if total <= room || len(flex) == 0 {
		return limits
	}
	remaining, left := room, len(flex)
	for _, i := range sortedByNeed(flex, need) {
		share := max(remaining/left, minFlexWidth)
		take := min(need[i], share)
		if take < need[i] {
			limits[i] = take
		}
		remaining -= take
		left--
	}
	return limits
}

// sortedByNeed orders column indexes by the width they need, smallest first.
func sortedByNeed(idx, need []int) []int {
	out := append([]int{}, idx...)
	sort.SliceStable(out, func(a, b int) bool { return need[out[a]] < need[out[b]] })
	return out
}
