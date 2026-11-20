package scheduler

import (
	"context"
	"strconv"
	"strings"

	"github.com/condatainer/condatainer/internal/utils"
)

// sinfoGpuFields are the fields of the GPU usage call. Partition, CPUs, Memory and Gres name the node
// type of a row, as the partition call does. GresUsed is per node, and Nodes is how many nodes share it.
// A value is cut to its width, so each width is the longest value the field can hold in practice.
var sinfoGpuFields = []squeueField{
	{"Partition", 64}, {"Nodes", 10}, {"CPUs", 12}, {"Memory", 16},
	{"Gres", 1024}, {"GresUsed", 1024}, {"StateLong", 16},
}

// addIdleGpus fills the idle GPUs of the node types that have GPUs. It makes one grouped sinfo call over the
// partitions that have GPUs, and none when no partition has. A call that fails leaves the figures unreported.
func (s *SlurmScheduler) addIdleGpus(ctx context.Context, limits []ResourceLimits) {
	var names []string
	for _, l := range limits {
		for _, t := range l.NodeTypes {
			if len(t.Gpus) > 0 {
				names = append(names, l.Partition)
				break
			}
		}
	}
	if len(names) == 0 {
		return
	}
	args := []string{"-e", "-h", "-p", strings.Join(names, ","), "-O", squeueFormat(sinfoGpuFields)}
	out, err := runCommandEnv(ctx, slurmToolEnv("SINFO_", false), "SLURM", "query-gpu-usage", s.sinfoCommand, args...)
	if err != nil {
		utils.PrintDebug("SLURM: idle GPUs not read: %v", err)
		return
	}
	parseSinfoGpuUsage(string(out), limits)
}

// gpuIdleSum is the idle GPUs of one node type by GPU type. unknown marks a node type with a row that could
// not be read, which has no figure.
type gpuIdleSum struct {
	byType  map[string]int
	unknown bool
}

// parseSinfoGpuUsage reads the rows of sinfoGpuFields and sets Idle on the GPU types of the node types.
//   - A row is matched to a node type by partition, CPUs, memory and GPUs.
//   - The idle GPUs of a row are the GPUs of each node less those in use, times its nodes. A node that is
//     not idle, mixed or allocated takes no job, so its GPUs are not idle.
//   - A node type with a row whose GPU fields were cut has no figure. A line that does not split leaves every
//     node type without one, since its nodes are not known.
func parseSinfoGpuUsage(output string, limits []ResourceLimits) {
	sums := map[string]*gpuIdleSum{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		rec, err := splitSqueueLine(line, sinfoGpuFields)
		if err != nil {
			return
		}
		v := rec.value
		cpuText, _ := splitLowerBound(v["CPUs"])
		cpus, _ := strconv.Atoi(cpuText)
		memText, _ := splitLowerBound(v["Memory"])
		memMB, _ := parseMemoryMB(memText)
		configured := parseSlurmGres(v["Gres"])
		if len(configured) == 0 {
			continue
		}

		key := gpuUsageKey(strings.TrimSuffix(v["Partition"], "*"), cpus, memMB, configured)
		sum := sums[key]
		if sum == nil {
			sum = &gpuIdleSum{byType: map[string]int{}}
			sums[key] = sum
		}
		if rec.cut["Gres"] || rec.cut["GresUsed"] {
			sum.unknown = true
			continue
		}
		for _, g := range configured {
			sum.byType[g.Type] += 0
		}
		if !slurmNodeTakesJobs(v["StateLong"]) {
			continue
		}
		nodes, _ := strconv.Atoi(v["Nodes"])
		used := map[string]int{}
		for _, g := range parseSlurmGres(v["GresUsed"]) {
			used[g.Type] = g.PerNode
		}
		for _, g := range configured {
			sum.byType[g.Type] += max(g.PerNode-used[g.Type], 0) * nodes
		}
	}

	for i := range limits {
		l := &limits[i]
		for j := range l.NodeTypes {
			t := &l.NodeTypes[j]
			sum := sums[gpuUsageKey(l.Partition, t.Cpus, t.MemMB, t.Gpus)]
			if sum == nil || sum.unknown {
				continue
			}
			for k := range t.Gpus {
				if idle, ok := sum.byType[t.Gpus[k].Type]; ok {
					t.Gpus[k].Idle = &idle
				}
			}
		}
	}
}

// gpuUsageKey names a node type of a partition, so the rows of the usage call find it.
func gpuUsageKey(partition string, cpus int, memMB int64, gpus []NodeGpu) string {
	return partition + "|" + strconv.Itoa(cpus) + "|" + strconv.FormatInt(memMB, 10) + "|" + gpuKey(gpus)
}

// slurmNodeTakesJobs reports whether a node state takes new jobs. A state may end in flag characters,
// such as "mixed-" for a node planned for a job and "drained*" for one that does not respond.
func slurmNodeTakesJobs(state string) bool {
	switch strings.ToLower(strings.TrimRight(strings.TrimSpace(state), "*~#%$@^-!")) {
	case "idle", "mixed", "allocated":
		return true
	}
	return false
}

// splitGres splits a GRES list at the commas outside parentheses, so the "(IDX:0-3,5)" of a GPU in use
// stays in its entry.
func splitGres(list string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ',':
			if depth == 0 {
				parts = append(parts, list[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, list[start:])
}
