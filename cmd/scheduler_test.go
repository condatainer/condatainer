package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
)

func TestSchedulerGroupHasInfoAndPartitions(t *testing.T) {
	if schedulerCmd.Runnable() {
		t.Error("the bare group must print help, so it has no Run")
	}
	got := map[string]bool{}
	for _, c := range schedulerCmd.Commands() {
		got[c.Name()] = true
	}
	for _, name := range []string{"info", "partitions"} {
		if !got[name] {
			t.Errorf("scheduler has no %q command", name)
		}
	}
}

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
