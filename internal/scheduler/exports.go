package scheduler

import (
	"fmt"
	"io"
	"strconv"
)

// exportPrefix names the copy of each resource variable that CondaTainer reads back.
// A user's own NCPU or MEM cannot shadow it.
const exportPrefix = "CNT_"

// resourceVar is one normalized resource variable.
type resourceVar struct{ name, value string }

// ResourceEnvVars returns the KEY=VALUE resource variables derived from rs.
//   - Scripts read the short names. CurrentJobResources reads the CNT_ copies.
//   - Always set: NNODE, NRANK and NCPU (the CPUs of one rank), each as CNT_<name> too, and OMP_NUM_THREADS.
//   - NRANK_PER_NODE only when TasksPerNode is known.
//   - MEM (MB per rank) and MEM_GB when memory is specified.
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

	// Total rank count: prefer the directly set Ntasks; fall back to topology derivation.
	ranks := directNtasks
	if ranks <= 0 {
		t := tasksPerNode
		if t <= 0 {
			t = 1
		}
		ranks = nodes * t
	}

	vars := []resourceVar{
		{"NNODE", strconv.Itoa(nodes)},
		{"NRANK", strconv.Itoa(ranks)},
		{"NCPU", strconv.Itoa(cpusPerTask)},
	}
	if tasksPerNode > 0 {
		vars = append(vars, resourceVar{"NRANK_PER_NODE", strconv.Itoa(tasksPerNode)})
	}
	if rs != nil {
		if memPerTaskMB := rs.GetMemPerTaskMB(); memPerTaskMB > 0 {
			vars = append(vars,
				resourceVar{"MEM", strconv.FormatInt(memPerTaskMB, 10)},
				resourceVar{"MEM_GB", strconv.FormatInt(memPerTaskMB/1024, 10)})
		}
	}

	env := make([]string, 0, 2*len(vars)+1)
	for _, v := range vars {
		env = append(env, v.name+"="+v.value, exportPrefix+v.name+"="+v.value)
	}
	return append(env, "OMP_NUM_THREADS="+strconv.Itoa(cpusPerTask))
}

// writeResourceExports writes the resource variables of rs as export lines.
//   - Every scheduler's job script calls it, so a job shows the same variables whichever scheduler runs it.
//   - OMP_NUM_THREADS replaces any value the scheduler set from its own request.
func writeResourceExports(w io.Writer, rs *ResourceSpec) {
	for _, kv := range ResourceEnvVars(rs) {
		fmt.Fprintf(w, "export %s\n", kv)
	}
	fmt.Fprintln(w, "")
}

// CurrentJobResources returns the resources of the job CondaTainer runs in, or nil outside a job.
//   - The CNT_ variables a CondaTainer job script exports come first, whichever scheduler ran the job.
//   - Otherwise the active scheduler's own variables are read.
//   - The exports describe the job as submitted. A job that a script inside it submits by hand inherits them.
func CurrentJobResources() *ResourceSpec {
	if res := resourcesFromExports(); res != nil {
		return res
	}
	if sched := ActiveScheduler(); sched != nil {
		return sched.GetJobResources()
	}
	return nil
}

// resourcesFromExports reads the CNT_ variables back into a ResourceSpec.
//   - CNT_NCPU marks them as present.
//   - MPI library variables stay ahead of CNT_NRANK and CNT_NRANK_PER_NODE, as for a scheduler's own variables.
//   - Memory per rank becomes memory per node over TasksPerNode, which GetMemPerTaskMB divides back.
func resourcesFromExports() *ResourceSpec {
	cpus := getEnvInt(exportPrefix + "NCPU")
	if cpus == nil {
		return nil
	}
	res := &ResourceSpec{CpusPerTask: *cpus}
	if v := getEnvInt(exportPrefix + "NNODE"); v != nil {
		res.Nodes = *v
	}
	if mpiLocal := getMpiLocalSize(); mpiLocal != nil {
		res.TasksPerNode = *mpiLocal
	} else if v := getEnvInt(exportPrefix + "NRANK_PER_NODE"); v != nil {
		res.TasksPerNode = *v
	}
	if mpiSize := getMpiCommSize(); mpiSize != nil {
		res.Ntasks = *mpiSize
	} else if v := getEnvInt(exportPrefix + "NRANK"); v != nil {
		res.Ntasks = *v
	}
	if mem := getEnvInt64(exportPrefix + "MEM"); mem != nil {
		res.MemPerNodeMB = *mem * int64(max(res.TasksPerNode, 1))
	}
	if n := getCudaDeviceCount(); n != nil && *n > 0 {
		res.Gpu = &GpuSpec{Count: *n}
	}
	return res
}
