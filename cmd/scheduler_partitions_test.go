package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
)

func TestSchedulerPartitionsCpuAndGpuExclude(t *testing.T) {
	c := schedulerPartitionsCmd
	if err := c.ParseFlags([]string{"--cpu", "--gpu"}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, name := range []string{"cpu", "gpu"} {
			_ = c.Flags().Set(name, "false")
			c.Flags().Lookup(name).Changed = false
		}
	}()
	if err := c.ValidateFlagGroups(); err == nil {
		t.Error("--cpu with --gpu was accepted")
	}
}

func TestPartitionRecord(t *testing.T) {
	l := scheduler.ResourceLimits{
		Partition: "gpu", State: "up", MaxTime: 72 * time.Hour, MaxNodes: 4,
		NodeTypes: []scheduler.NodeType{
			{Nodes: 6, Cpus: 112, MemMB: 2048000, MemLowerBound: true, Gpus: []scheduler.NodeGpu{{Type: "h100", PerNode: 8}}},
		},
	}
	got := partitionRecord(l, nil)
	if got.Name != "gpu" || got.State != "up" || got.MaxTimeS != 259200 || got.MaxNodes != 4 || got.Default {
		t.Errorf("partition = %+v", got)
	}
	if len(got.NodeTypes) != 1 || got.NodeTypes[0].Nodes != 6 || !got.NodeTypes[0].MemLowerBound ||
		len(got.NodeTypes[0].Gpus) != 1 || got.NodeTypes[0].Gpus[0].PerNode != 8 {
		t.Errorf("node types = %+v", got.NodeTypes)
	}

	// The nodes that are up are reported only when the scheduler gives them, and 0 is a figure.
	zero := 0
	withState := partitionRecord(scheduler.ResourceLimits{Partition: "s",
		NodeTypes: []scheduler.NodeType{{Nodes: 4, NodesUp: &zero}, {Nodes: 2}}}, nil)
	if up := withState.NodeTypes[0].NodesUp; up == nil || *up != 0 || withState.NodeTypes[1].NodesUp != nil {
		t.Errorf("nodes up = %+v", withState.NodeTypes)
	}

	if unlimited := partitionRecord(scheduler.ResourceLimits{Partition: "u", TimeUnlimited: true}, nil); !unlimited.TimeUnlimited || unlimited.MaxTimeS != 0 {
		t.Errorf("unlimited partition = %+v", unlimited)
	}

	// A scheduler with no node types gives one, from the largest host and the partition's GPU types.
	fallback := partitionRecord(scheduler.ResourceLimits{Partition: "q", MaxCpusPerNode: 32, MaxMemMBPerNode: 65536},
		[]scheduler.GpuInfo{{Type: "v100", Total: 4}})
	if len(fallback.NodeTypes) != 1 || fallback.NodeTypes[0].Cpus != 32 ||
		len(fallback.NodeTypes[0].Gpus) != 1 || fallback.NodeTypes[0].Gpus[0].Type != "v100" || fallback.NodeTypes[0].Gpus[0].PerNode != 0 {
		t.Errorf("fallback node types = %+v", fallback.NodeTypes)
	}
}

func TestSelectPartitionsFiltersAndOrders(t *testing.T) {
	info := &scheduler.ClusterInfo{
		Limits: []scheduler.ResourceLimits{
			{Partition: "old", State: "down"},
			{Partition: "cpu", State: "up"},
			{Partition: "gpu", State: "up", NodeTypes: []scheduler.NodeType{{Gpus: []scheduler.NodeGpu{{Type: "h100", PerNode: 8}}}}},
		},
	}
	names := func(ls []scheduler.ResourceLimits) string {
		var s []string
		for _, l := range ls {
			s = append(s, l.Partition)
		}
		return strings.Join(s, ",")
	}
	all, _ := selectPartitions(info, false, false)
	if got := names(all); got != "cpu,gpu,old" {
		t.Errorf("all = %s, want the partition that is not up last", got)
	}
	cpu, _ := selectPartitions(info, true, false)
	if got := names(cpu); got != "cpu,old" {
		t.Errorf("--cpu = %s", got)
	}
	gpu, _ := selectPartitions(info, false, true)
	if got := names(gpu); got != "gpu" {
		t.Errorf("--gpu = %s", got)
	}
}

// partitionLister is a scheduler that reports partitions, and says it runs inside a job.
type partitionLister struct {
	scheduler.Scheduler
	info *scheduler.ClusterInfo
}

func (partitionLister) GetType() scheduler.SchedulerType  { return "FAKE" }
func (partitionLister) GetBinary() string                 { return "/fake/bin/fsub" }
func (partitionLister) GetVersion(context.Context) string { return "1.0" }
func (partitionLister) IsAvailable() bool                 { return true }
func (partitionLister) IsInsideJob() bool                 { return true }
func (p partitionLister) GetClusterInfo(context.Context) (*scheduler.ClusterInfo, error) {
	return p.info, nil
}

// Listing partitions reads state and submits nothing, so it works inside a scheduled job.
func TestSchedulerPartitionsWorkInsideAJob(t *testing.T) {
	t.Setenv("SLURM_JOB_ID", "1")
	scheduler.SetActiveScheduler(partitionLister{info: &scheduler.ClusterInfo{Limits: []scheduler.ResourceLimits{
		{Partition: "gpu", State: "up", NodeTypes: []scheduler.NodeType{{Nodes: 2, Cpus: 8}}},
	}}})
	t.Cleanup(scheduler.ClearActiveScheduler)
	schedulerPartitionsJSON = true
	t.Cleanup(func() { schedulerPartitionsJSON = false })

	c := schedulerPartitionsCmd
	c.SetContext(context.Background())
	var out bytes.Buffer
	c.SetOut(&out)
	if err := runSchedulerPartitions(c, nil); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Scheduler struct {
			Status string `json:"status"`
		} `json:"scheduler"`
		Partitions []struct {
			Name string `json:"name"`
		} `json:"partitions"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(doc.Partitions) != 1 || doc.Partitions[0].Name != "gpu" || doc.Scheduler.Status != "inside_job" {
		t.Errorf("document = %+v, want the partition and inside_job", doc)
	}
}
