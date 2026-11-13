package report

// Scheduler is the job scheduler of the system. Type and Binary are absent inside a scheduled job,
// where no scheduler is looked up. Available and InsideJob are always written.
type Scheduler struct {
	Type      string `json:"type,omitempty"`
	Binary    string `json:"binary,omitempty"`
	Version   string `json:"version,omitempty"`
	Available bool   `json:"available"`
	InsideJob bool   `json:"inside_job"`
}

// SchedulerInfo is the document of the command that shows the scheduler. Scheduler is absent when none is found.
type SchedulerInfo struct {
	Doc
	Scheduler *Scheduler    `json:"scheduler,omitempty"`
	Problems  List[Problem] `json:"problems"`
}

// NodeGpu is one GPU type and how many of it one node carries. PerNode is absent when the scheduler does not say.
type NodeGpu struct {
	Type    string `json:"type"`
	PerNode int    `json:"per_node,omitempty"`
}

// NodeType is a group of a partition's nodes with the same CPUs, GPUs and memory class.
// NodesUp is how many of Nodes are allocated or idle, so not down or drained. It is absent when the scheduler does not report node state.
// A lower-bound flag says the figure is the smallest of unequal nodes, and the largest node has more.
type NodeType struct {
	Nodes          int           `json:"nodes,omitempty"`
	NodesUp        *int          `json:"nodes_up,omitempty"`
	Cpus           int           `json:"cpus,omitempty"`
	MemMB          int64         `json:"mem_mb,omitempty"`
	CpusLowerBound bool          `json:"cpus_lower_bound"`
	MemLowerBound  bool          `json:"mem_lower_bound"`
	Gpus           List[NodeGpu] `json:"gpus"`
}

// Partition is a partition, or a queue, with its node types.
//   - MaxNodes and MaxTimeS are the most one job can use, and absent when there is no limit
//     or the scheduler does not report it. TimeUnlimited says the scheduler reports no time limit.
//   - The node count of the partition and the total of a GPU type are sums over NodeTypes.
type Partition struct {
	Name          string         `json:"name"`
	State         string         `json:"state,omitempty"`
	Default       bool           `json:"default"`
	MaxNodes      int            `json:"max_nodes,omitempty"`
	MaxTimeS      int64          `json:"max_time_s,omitempty"`
	TimeUnlimited bool           `json:"time_unlimited"`
	NodeTypes     List[NodeType] `json:"node_types"`
}

// Partitions is the document of the command that lists the partitions of the scheduler.
type Partitions struct {
	Doc
	Scheduler  *Scheduler      `json:"scheduler,omitempty"`
	Partitions List[Partition] `json:"partitions"`
	Problems   List[Problem]   `json:"problems"`
}

// JobGpu is a GPU type a job holds or asks for, and how many.
type JobGpu struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// JobResources is what a job asks for while it waits and holds once it runs. For an array it is per task.
// MemMB is the total over every node. Ranks and CpusPerRank are set for a job of several processes.
// RanksPerNode is set only when the user fixed it, so every node has that many ranks.
// A figure the scheduler does not report is absent.
type JobResources struct {
	Cpus         int          `json:"cpus,omitempty"`
	MemMB        int64        `json:"mem_mb,omitempty"`
	Nodes        int          `json:"nodes,omitempty"`
	Gpus         List[JobGpu] `json:"gpus"`
	Ranks        int          `json:"ranks,omitempty"`
	CpusPerRank  int          `json:"cpus_per_rank,omitempty"`
	RanksPerNode int          `json:"ranks_per_node,omitempty"`
}

// JobDependency is a parent a job waits for. Type is the scheduler's own word for the rule.
// Satisfied is true once the rule holds and Failed once it can never hold.
type JobDependency struct {
	Type      string `json:"type"`
	JobID     string `json:"job_id,omitempty"`
	Satisfied bool   `json:"satisfied"`
	Failed    bool   `json:"failed,omitempty"`
}

// JobTasks are the tasks of an array, with their IDs in the form `jobs` takes.
// A listing sees only the tasks in the queue. A lookup by ID has Total and the counts of the tasks
// that ended too, with their IDs, and UnknownIDs holds tasks whose state could not be read.
type JobTasks struct {
	Running      int          `json:"running"`
	Pending      int          `json:"pending"`
	RunningIDs   List[string] `json:"running_ids"`
	PendingIDs   List[string] `json:"pending_ids"`
	Total        *int         `json:"total,omitempty"`
	Success      *int         `json:"success,omitempty"`
	Failed       *int         `json:"failed,omitempty"`
	Cancelled    *int         `json:"cancelled,omitempty"`
	SuccessIDs   List[string] `json:"success_ids,omitempty"`
	FailedIDs    List[string] `json:"failed_ids,omitempty"`
	CancelledIDs List[string] `json:"cancelled_ids,omitempty"`
	UnknownIDs   List[string] `json:"unknown_ids,omitempty"`
}

// JobUsage is what a job that ended used. CpuTimeS is the CPU time of all its processes.
// PeakMemMB is the highest memory one step reached, a lower bound, and absent when the scheduler does not report it.
type JobUsage struct {
	CpuTimeS  *int64 `json:"cpu_time_s,omitempty"`
	PeakMemMB int64  `json:"peak_mem_mb,omitempty"`
}

// Job is a job as the scheduler reports it.
//   - ID is the form `jobs` takes. A task of an array is "<array>_<task>", with ArrayID and TaskID.
//   - State is pending, running, success, failed, cancelled, notfound or unknown. Suspended marks a running job that is stopped.
//   - Times are RFC 3339. StartTime is absent for a job that has not started, and EndTime for one that has not ended.
//   - An array has Tasks, and no StartTime, EndTime, QueuedS or ElapsedS, since its tasks have their own.
//   - TimeLimitS is absent when the job has no limit or the scheduler does not say. TimeUnlimited says there is none.
//   - Stderr repeats Stdout when both are one file.
//   - Failure is why a failed job failed: nonzero_exit, timeout, out_of_memory, node_failure, preempted or deadline.
//   - ExitCode and ExitSignal are those of the job's own program, and absent when the scheduler ended the job or cancelled it.
//   - Source is where the record came from: queue or history. It is absent for a job the scheduler has no record of.
type Job struct {
	ID            string              `json:"id"`
	ArrayID       string              `json:"array_id,omitempty"`
	TaskID        string              `json:"task_id,omitempty"`
	State         string              `json:"state"`
	Suspended     bool                `json:"suspended,omitempty"`
	Name          string              `json:"name"`
	Partitions    List[string]        `json:"partitions"`
	Reason        string              `json:"reason,omitempty"`
	DependsOn     List[JobDependency] `json:"depends_on,omitempty"`
	NodeList      List[string]        `json:"node_list,omitempty"`
	SubmitTime    string              `json:"submit_time,omitempty"`
	StartTime     string              `json:"start_time,omitempty"`
	EndTime       string              `json:"end_time,omitempty"`
	QueuedS       *int64              `json:"queued_s,omitempty"`
	ElapsedS      *int64              `json:"elapsed_s,omitempty"`
	TimeLimitS    int64               `json:"time_limit_s,omitempty"`
	TimeUnlimited bool                `json:"time_unlimited,omitempty"`
	Resources     JobResources        `json:"resources"`
	Stdout        string              `json:"stdout,omitempty"`
	Stderr        string              `json:"stderr,omitempty"`
	Tasks         *JobTasks           `json:"tasks,omitempty"`
	Failure       string              `json:"failure,omitempty"`
	ExitCode      *int                `json:"exit_code,omitempty"`
	ExitSignal    *int                `json:"exit_signal,omitempty"`
	Usage         *JobUsage           `json:"usage,omitempty"`
	Source        string              `json:"source,omitempty"`
}

// Jobs is the document of the command that lists jobs.
type Jobs struct {
	Doc
	Scheduler *Scheduler    `json:"scheduler,omitempty"`
	Jobs      List[Job]     `json:"jobs"`
	Problems  List[Problem] `json:"problems"`
}

// CancelJob is a job the cancel command picked, as it was before the cancel.
//   - Cancel is sent, notfound or error. It is not the job's state: a job that was just sent a cancel may still read running.
//   - Error is the scheduler's message, set when Cancel is error.
//   - Cancel is absent when nothing was cancelled.
type CancelJob struct {
	Job
	Cancel string `json:"cancel,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Cancel is the document of the command that cancels jobs.
type Cancel struct {
	Doc
	Scheduler *Scheduler      `json:"scheduler,omitempty"`
	Jobs      List[CancelJob] `json:"jobs"`
	Problems  List[Problem]   `json:"problems"`
}
