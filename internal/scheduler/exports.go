package scheduler

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// exportPrefix names the copy of each resource variable that CondaTainer reads back.
// A user's own NCPU or MEM cannot shadow it.
const exportPrefix = "CNT_"

// LoginNodeVar names the variable that records the login node a job was submitted from.
const LoginNodeVar = "CNT_LOGIN_NODE"

// LoginNode returns the login node a job is tied to, or "" when none is known.
//   - A recorded value that is a plain host name wins, so a chain of jobs keeps the first login node.
//   - Outside a job it is this node's name.
//   - Inside a job with none recorded it is "", since a compute node is not a login node.
func LoginNode() string {
	if node := os.Getenv(LoginNodeVar); validateNodeName(node) == nil {
		return node
	}
	if IsInsideJob() {
		return ""
	}
	if node, err := os.Hostname(); err == nil && validateNodeName(node) == nil {
		return node
	}
	return ""
}

// writeLoginNodeExport writes the login node as an export line, when one is known.
//   - Every scheduler's job script calls it, with or without a resource spec.
func writeLoginNodeExport(w io.Writer) {
	if node := LoginNode(); node != "" {
		fmt.Fprintf(w, "export %s=%s\n\n", LoginNodeVar, node)
	}
}

// resourceVar is one normalized resource variable.
type resourceVar struct{ name, value string }

// ResourceEnvVars returns the KEY=VALUE resource variables derived from rs.
//   - Scripts read the short names. CurrentJobResources reads the CNT_ copies.
//   - Always set: NNODE, NRANK and NCPU (the CPUs of one rank), each as CNT_<name> too.
//   - OMP_NUM_THREADS is not one of them. Only a run sets it, when it is unset (RunEnvVars).
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
	return env
}

// resourceVarNames is every variable ResourceEnvVars can write, before its CNT_ copy.
var resourceVarNames = []string{"NNODE", "NRANK", "NCPU", "NRANK_PER_NODE", "MEM", "MEM_GB"}

// writeResourceExports writes the resource variables of rs as export lines.
//   - Every scheduler's job script calls it, so a job shows the same variables whichever scheduler runs it.
//   - One unset line comes first, so a variable the job inherited from its submitter and rs leaves out does not survive.
func writeResourceExports(w io.Writer, rs *ResourceSpec) {
	names := make([]string, 0, 2*len(resourceVarNames))
	for _, name := range resourceVarNames {
		names = append(names, name, exportPrefix+name)
	}
	fmt.Fprintf(w, "unset %s\n", strings.Join(names, " "))
	for _, kv := range ResourceEnvVars(rs) {
		fmt.Fprintf(w, "export %s\n", kv)
	}
	fmt.Fprintln(w, "")
}

// CurrentJobResources returns the CPUs, memory and GPUs of the job CondaTainer runs in, or nil outside a job.
//   - The CNT_ variables a CondaTainer job script exports come first, whichever scheduler ran the job.
//   - Otherwise the active scheduler's own variables are read, or those of the scheduler whose job variables are set.
//   - The exports describe the job as submitted. A job that a script inside it submits by hand inherits them.
//   - Ntasks is the rank count the scheduler reports, 0 when it does not say.
func CurrentJobResources() *ResourceSpec {
	if res := resourcesFromExports(); res != nil {
		res.Ntasks = jobRankCount()
		return res
	}
	if sched := ActiveScheduler(); sched != nil {
		return sched.GetJobResources()
	}
	return jobEnvReader().GetJobResources()
}

// jobEnvReader is the reader of the scheduler whose job variables are set, for a process that has no active scheduler.
//   - A container in a job has no scheduler binary, and the job's variables are still in its environment.
func jobEnvReader() interface{ GetJobResources() *ResourceSpec } {
	switch {
	case os.Getenv("SLURM_JOB_ID") != "":
		return &SlurmScheduler{}
	case os.Getenv("PBS_JOBID") != "":
		return &PbsScheduler{}
	case os.Getenv("LSB_JOBID") != "":
		return &LsfScheduler{}
	}
	return &HTCondorScheduler{}
}

// resourcesFromExports reads the CNT_ variables back into a ResourceSpec.
//   - CNT_NCPU marks them as present.
//   - Only the CPUs, the memory and the GPUs are read. The rank count is jobRankCount's.
func resourcesFromExports() *ResourceSpec {
	cpus := getEnvInt(exportPrefix + "NCPU")
	if cpus == nil {
		return nil
	}
	res := &ResourceSpec{CpusPerTask: *cpus}
	if mem := getEnvInt64(exportPrefix + "MEM"); mem != nil {
		res.MemPerNodeMB = *mem
	}
	if n := jobGpuCount(); n != nil && *n > 0 {
		res.Gpu = &GpuSpec{Count: *n}
	}
	return res
}

// jobRankCount is the number of tasks of the job, or 0 when nothing says.
//   - The CNT_NRANK a CondaTainer job script exports comes first, since PBS Pro sets no variable with its ranks.
//   - Otherwise SLURM_NTASKS, the lines of the PBS node file, then LSF's slots. Each LSF slot is one rank.
func jobRankCount() int {
	if v := getEnvInt(exportPrefix + "NRANK"); v != nil {
		return *v
	}
	if v := getEnvInt("SLURM_NTASKS"); v != nil {
		return *v
	}
	if ranks, _ := pbsNodeFile(); ranks > 0 {
		return ranks
	}
	for _, name := range []string{"LSB_DJOB_NUMPROC", "LSB_MAX_NUM_PROCESSORS"} {
		if v := getEnvInt(name); v != nil {
			return *v
		}
	}
	return 0
}

// InMultiRankJob reports whether the job this process runs in has more than one rank.
func InMultiRankJob() bool {
	return IsInsideJob() && jobRankCount() > 1
}

// ompThreadsSet reports whether OMP_NUM_THREADS already has a value.
func ompThreadsSet() bool {
	return os.Getenv("OMP_NUM_THREADS") != ""
}

// SizeEnvVars returns the size of one rank as variables: NCPU, MEM and MEM_GB, each with its CNT_ copy.
//   - The rank variables are not in it. Only the job script header exports them.
func SizeEnvVars(rs *ResourceSpec) []string {
	keep := map[string]bool{"NCPU": true, "MEM": true, "MEM_GB": true}
	var env []string
	for _, kv := range ResourceEnvVars(rs) {
		name, _, _ := strings.Cut(kv, "=")
		if keep[strings.TrimPrefix(name, exportPrefix)] {
			env = append(env, kv)
		}
	}
	return env
}

// RunEnvVars returns the resource variables of a script run in place, from the resources rs gives it.
//   - They are SizeEnvVars and OMP_NUM_THREADS.
//   - An OMP_NUM_THREADS that is already set stays, unless forceThreads.
func RunEnvVars(rs *ResourceSpec, forceThreads bool) []string {
	env := SizeEnvVars(rs)
	if forceThreads || !ompThreadsSet() {
		cpus := 1
		if rs != nil && rs.CpusPerTask > 0 {
			cpus = rs.CpusPerTask
		}
		env = append(env, "OMP_NUM_THREADS="+strconv.Itoa(cpus))
	}
	return env
}

// MultiRankEnvVars returns what a script in a job with more than one rank gets: OMP_NUM_THREADS when it is not set, and nothing else.
//   - The value is the CPUs of one rank: CNT_NCPU, else SLURM_CPUS_PER_TASK, else 1.
func MultiRankEnvVars() []string {
	if ompThreadsSet() {
		return nil
	}
	cpus := 1
	if v := getEnvInt(exportPrefix + "NCPU"); v != nil {
		cpus = *v
	} else if v := getEnvInt("SLURM_CPUS_PER_TASK"); v != nil {
		cpus = *v
	}
	return []string{"OMP_NUM_THREADS=" + strconv.Itoa(cpus)}
}
