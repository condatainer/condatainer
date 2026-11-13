package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var (
	schedulerInfoJSON       bool
	schedulerPartitionsJSON bool
)

var schedulerCmd = &cobra.Command{
	Use:   "scheduler",
	Short: "Show the job scheduler, its partitions and your jobs",
	Long: `Show the job scheduler of this system, and your jobs on it.

- info shows the scheduler and whether it takes jobs.
- partitions lists the partitions/queues you can pass to --partition.
- jobs lists your jobs, or shows one job in full.
- cancel cancels your jobs.`,
}

var schedulerInfoCmd = &cobra.Command{
	SilenceUsage: true, // Runtime errors should not show usage
	Use:          "info",
	Args:         cobra.NoArgs,
	Short:        "Show the detected job scheduler",
	Long: `Show the detected job scheduler: type, binary path, version, and availability.

Inside a scheduled job it reports the scheduler as unavailable.`,
	Example: `  condatainer scheduler info`,
	RunE:    runSchedulerInfo,
}

var schedulerPartitionsCmd = &cobra.Command{
	SilenceUsage: true, // Runtime errors should not show usage
	Use:          "partitions",
	Args:         cobra.NoArgs,
	Short:        "List the partitions of the scheduler",
	Long: `List the partitions/queues you can pass to --partition.

A partition has a row for each type of node in it.
A type is a group of nodes with the same CPUs and GPUs.

- A * after a partition name marks the default partition.
- AVAIL is whether the partition takes jobs. Partitions that are not up are listed last.
- NODES, CPUS and MEM are per node type. A + marks the smallest of nodes that differ.
- MAXTIME is the longest one job can run. inf means no limit, - means none is reported.
- MAXNODES is the most nodes one job can use. It is left out when no partition has a limit.
- GPUS is each GPU type of the node type, as type:count per node.`,
	Example: `  condatainer scheduler partitions
  condatainer scheduler partitions --gpu  # Only GPU partitions
  condatainer scheduler partitions --cpu  # Only CPU-only partitions`,
	RunE: runSchedulerPartitions,
}

var errNoScheduler = errors.New("no job scheduler detected on this system")

// lookupScheduler finds the scheduler to report on. Inside a scheduled job it looks for none and
// reports insideJob, so no binary is probed there.
func lookupScheduler() (sched scheduler.Scheduler, insideJob bool, err error) {
	if scheduler.IsInsideJob() {
		return nil, true, nil
	}
	// Use the already-initialized scheduler if there is one.
	if sched := scheduler.ActiveScheduler(); sched != nil {
		return sched, false, nil
	}
	sched, err = scheduler.DetectSchedulerWithBinary(scheduler.Bin())
	if err != nil {
		return nil, false, errNoScheduler
	}
	return sched, false, nil
}

// schedulerRecord describes the scheduler for the JSON documents.
func schedulerRecord(ctx context.Context, sched scheduler.Scheduler, insideJob bool) *report.Scheduler {
	if sched == nil {
		return &report.Scheduler{InsideJob: insideJob}
	}
	inside := insideJob || sched.IsInsideJob()
	return &report.Scheduler{
		Type:      strings.ToLower(string(sched.GetType())),
		Binary:    sched.GetBinary(),
		Version:   sched.GetVersion(ctx),
		Available: sched.IsAvailable() && !inside,
		InsideJob: inside,
	}
}

func printInsideJob() {
	utils.PrintMessage("Scheduler Status: %s", utils.StyleWarning("Unavailable (inside job)"))
	utils.PrintMessage("")
	utils.PrintMessage("You are currently inside a scheduled job; job submission is disabled to prevent nested submissions.")
}

func printNoScheduler() {
	utils.PrintMessage("Scheduler Status: %s", utils.StyleError("Not Found"))
}

func runSchedulerInfo(cmd *cobra.Command, args []string) error {
	sched, insideJob, err := lookupScheduler()

	if schedulerInfoJSON {
		doc := &report.SchedulerInfo{Problems: report.List[report.Problem]{}}
		if err != nil {
			doc.Problems = append(doc.Problems, report.Problem{Message: err.Error()})
		} else {
			doc.Scheduler = schedulerRecord(cmd.Context(), sched, insideJob)
		}
		return report.Write(cmd.OutOrStdout(), doc)
	}

	if insideJob {
		printInsideJob()
		return nil
	}
	if err != nil {
		printNoScheduler()
		return nil
	}

	// Structured output has no [CNT] prefix.
	fmt.Println("Scheduler Information:")
	fmt.Printf("  Type:      %s\n", string(sched.GetType()))
	fmt.Printf("  Binary:    %s\n", sched.GetBinary())
	if version := sched.GetVersion(cmd.Context()); version != "" {
		fmt.Printf("  Version:   %s\n", version)
	}

	switch {
	case sched.IsInsideJob():
		fmt.Printf("  Status:    %s (inside job)\n", utils.StyleError("Unavailable"))
		fmt.Println()
		fmt.Println("You are currently inside a scheduled job (detected via environment).")
		fmt.Println("Job submission is disabled to prevent nested job submissions.")
	case sched.IsAvailable():
		fmt.Printf("  Status:    %s\n", utils.StyleSuccess("Available"))
		fmt.Println()
		fmt.Println("The scheduler is available and ready for job submission.")
	default:
		fmt.Printf("  Status:    %s\n", utils.StyleError("Unavailable"))
		fmt.Println()
		fmt.Println("Scheduler detected but not available for job submission.")
	}
	return nil
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
	cpuOnly, _ := cmd.Flags().GetBool("cpu")
	gpuOnly, _ := cmd.Flags().GetBool("gpu")

	sched, insideJob, err := lookupScheduler()
	if err == nil && !insideJob && sched.IsInsideJob() {
		insideJob = true
	}

	if schedulerPartitionsJSON {
		doc := &report.Partitions{Partitions: report.List[report.Partition]{}, Problems: report.List[report.Problem]{}}
		switch {
		case err != nil:
			doc.Problems = append(doc.Problems, report.Problem{Message: err.Error()})
		case insideJob:
			doc.Scheduler = schedulerRecord(cmd.Context(), sched, true)
		default:
			doc.Scheduler = schedulerRecord(cmd.Context(), sched, false)
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
	if insideJob {
		printInsideJob()
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

func init() {
	rootCmd.AddCommand(schedulerCmd)
	schedulerCmd.AddCommand(schedulerInfoCmd)
	schedulerCmd.AddCommand(schedulerPartitionsCmd)
	jsonFlag(schedulerInfoCmd.Flags(), &schedulerInfoJSON)
	jsonFlag(schedulerPartitionsCmd.Flags(), &schedulerPartitionsJSON)
	schedulerPartitionsCmd.Flags().Bool("cpu", false, "Show only CPU-only partitions (no GPUs)")
	schedulerPartitionsCmd.Flags().Bool("gpu", false, "Show only GPU partitions")
	schedulerPartitionsCmd.MarkFlagsMutuallyExclusive("cpu", "gpu")
}
