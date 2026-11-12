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
// NodesUp is how many of Nodes are up and take jobs, and absent when the scheduler does not report node state.
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
