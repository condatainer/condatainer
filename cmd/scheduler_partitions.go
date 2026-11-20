package cmd

import (
	"os"
	"sort"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var schedulerPartitionsCmd = &cobra.Command{
	SilenceUsage: true, // Runtime errors should not show usage
	Use:          "partitions",
	Args:         cobra.NoArgs,
	Short:        "List the partitions of the scheduler",
	Long: `List the partitions/queues you can pass to --partition.

A partition has a row for each type of node in it.
A type is a group of nodes with the same CPUs and GPUs.

- A * after a partition name marks the default partition.
- Partitions that are not up are listed last.

Columns:
- AVAIL is whether the partition takes jobs.
- NODES, CPUS and MEM are per node type.
- MAXTIME is the longest one job can run.
- MAXNODES is the most nodes one job can use. Left out if none has a limit.
- GPUS is each GPU type of the node type, as type:count per node.

In a cell:
- A + marks the smallest of nodes that differ.
- In MAXTIME, inf means no limit and - means none is reported.`,
	Example: `  condatainer scheduler partitions
  condatainer scheduler partitions --gpu
  condatainer scheduler partitions --cpu
  condatainer scheduler partitions --host`,
	RunE: runSchedulerPartitions,
}

func init() {
	jsonFlag(schedulerPartitionsCmd.Flags(), &schedulerPartitionsJSON)
	schedulerPartitionsCmd.Flags().Bool("cpu", false, "Show only CPU-only partitions (no GPUs)")
	schedulerPartitionsCmd.Flags().Bool("gpu", false, "Show only GPU partitions")
	schedulerPartitionsCmd.MarkFlagsMutuallyExclusive("cpu", "gpu")
	addHostFlags(schedulerPartitionsCmd.Flags())
}

// selectPartitions applies --cpu and --gpu and puts the partitions that are not up last,
// each group in the scheduler's order. It also returns the GPU types of each partition.
func selectPartitions(info *scheduler.ClusterInfo, cpuOnly, gpuOnly bool) ([]scheduler.ResourceLimits, map[string][]scheduler.GpuInfo) {
	gpusByPartition := make(map[string][]scheduler.GpuInfo)
	for _, gpu := range info.AvailableGpus {
		name := scheduler.PartitionName(gpu.Partition)
		gpusByPartition[name] = append(gpusByPartition[name], gpu)
	}

	var shown []scheduler.ResourceLimits
	for _, limit := range info.Limits {
		hasGpu := len(gpusByPartition[scheduler.PartitionName(limit.Partition)]) > 0
		for _, t := range limit.NodeTypes {
			hasGpu = hasGpu || len(t.Gpus) > 0
		}
		if (cpuOnly && hasGpu) || (gpuOnly && !hasGpu) {
			continue
		}
		shown = append(shown, limit)
	}
	sort.SliceStable(shown, func(i, j int) bool { return shown[i].IsUp() && !shown[j].IsUp() })
	return shown, gpusByPartition
}

// partitionRecord describes a partition for the JSON document.
func partitionRecord(l scheduler.ResourceLimits, fallbackGpus []scheduler.GpuInfo) report.Partition {
	p := report.Partition{
		Name:          scheduler.PartitionName(l.Partition),
		State:         l.State,
		Default:       l.Default,
		MaxNodes:      l.MaxNodes,
		MaxTimeS:      int64(l.MaxTime.Seconds()),
		TimeUnlimited: l.TimeUnlimited,
		NodeTypes:     report.List[report.NodeType]{},
	}
	for _, t := range scheduler.PartitionNodeTypes(l, fallbackGpus) {
		rec := report.NodeType{
			Nodes: t.Nodes, NodesUp: t.NodesUp, Cpus: t.Cpus, MemMB: t.MemMB,
			CpusLowerBound: t.CpusLowerBound, MemLowerBound: t.MemLowerBound,
			Gpus: report.List[report.NodeGpu]{},
		}
		for _, g := range t.Gpus {
			rec.Gpus = append(rec.Gpus, report.NodeGpu{Type: g.Type, PerNode: g.PerNode})
		}
		p.NodeTypes = append(p.NodeTypes, rec)
	}
	return p
}

func runSchedulerPartitions(cmd *cobra.Command, args []string) error {
	if done, err := runOnHostIfAsked(cmd, args); done {
		return err
	}
	cpuOnly, _ := cmd.Flags().GetBool("cpu")
	gpuOnly, _ := cmd.Flags().GetBool("gpu")

	sched, err := lookupQueryScheduler()

	if schedulerPartitionsJSON {
		doc := &report.Partitions{Partitions: report.List[report.Partition]{}, Problems: report.List[report.Problem]{}}
		switch {
		case err != nil:
			doc.Scheduler = schedulerRecord(nil, "")
			doc.Problems = append(doc.Problems, report.Problem{Message: err.Error()})
		default:
			doc.Scheduler = schedulerRecord(sched, sched.GetVersion(cmd.Context()))
			info, infoErr := sched.GetClusterInfo(cmd.Context())
			if infoErr != nil {
				doc.Problems = append(doc.Problems, report.Problem{Message: infoErr.Error()})
				break
			}
			if info == nil || len(info.Limits) == 0 {
				doc.Problems = append(doc.Problems, report.Problem{Message: "this scheduler reported no partitions"})
				break
			}
			shown, gpus := selectPartitions(info, cpuOnly, gpuOnly)
			for _, limit := range shown {
				doc.Partitions = append(doc.Partitions, partitionRecord(limit, gpus[scheduler.PartitionName(limit.Partition)]))
			}
		}
		return report.Write(cmd.OutOrStdout(), doc)
	}

	if err != nil {
		printNoScheduler()
		return nil
	}

	info, err := sched.GetClusterInfo(cmd.Context())
	if err != nil {
		return err
	}
	if info == nil || len(info.Limits) == 0 {
		utils.PrintWarning("This scheduler reported no partitions")
		return nil
	}
	shown, gpus := selectPartitions(info, cpuOnly, gpuOnly)
	if len(shown) == 0 {
		if gpuOnly {
			utils.PrintWarning("No GPU partitions available")
		} else {
			utils.PrintWarning("No CPU-only partitions available")
		}
		return nil
	}

	scheduler.WritePartitionTable(os.Stdout, shown, gpus)
	return nil
}
