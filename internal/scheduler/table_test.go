package scheduler

import (
	"bytes"
	"regexp"
	"testing"
	"time"
)

func TestFormatMem(t *testing.T) {
	tests := map[int64]string{
		512:      "512M",
		1000:     "1000M",
		8192:     "8192M",
		10239:    "10239M",
		10240:    "10G",
		766000:   "748G",
		507000:   "495G",
		2048000:  "2000G",
		3045514:  "2974G",
		10485759: "10240G",
		10485760: "10T",
		12582912: "12T",
		11010048: "10.5T",
	}
	for mb, want := range tests {
		if got := formatMem(mb); got != want {
			t.Errorf("formatMem(%d) = %q, want %q", mb, got, want)
		}
	}
}

func TestFormatHM(t *testing.T) {
	tests := map[time.Duration]string{
		8 * time.Hour:              "08:00",
		72 * time.Hour:             "72:00",
		time.Hour + 5*time.Minute:  "01:05",
		7 * 24 * time.Hour:         "168:00",
		0:                          "00:00",
		59*time.Minute + time.Hour: "01:59",
	}
	for d, want := range tests {
		if got := formatHM(d); got != want {
			t.Errorf("formatHM(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPartitionTable(t *testing.T) {
	limits := []ResourceLimits{
		{Partition: "cpubase_interac", State: "up", MaxTime: 8 * time.Hour, Nodes: 702, MaxCpusPerNode: 192, MaxMemMBPerNode: 766000,
			NodeTypes: []NodeType{{Nodes: 702, Cpus: 192, MemMB: 766000}}},
		{Partition: "cpubackfill", State: "up", Default: true, MaxTime: 24 * time.Hour, Nodes: 709,
			NodeTypes: []NodeType{{Nodes: 709, Cpus: 192, MemMB: 766000, MemLowerBound: true}}},
		{Partition: "gpubase_bynode_b4", State: "up", MaxTime: 72 * time.Hour,
			NodeTypes: []NodeType{
				{Nodes: 6, Cpus: 112, MemMB: 2048000, Gpus: []NodeGpu{{Type: "h100", PerNode: 8}}},
				{Nodes: 5, Cpus: 112, MemMB: 2048000, Gpus: []NodeGpu{
					{Type: "nvidia_h100_80gb_hbm3_3g.40gb", PerNode: 8}, {Type: "nvidia_h100_80gb_hbm3_1g.10gb", PerNode: 16}}},
				{Nodes: 4, Cpus: 96, MemMB: 507000, Gpus: []NodeGpu{{Type: "mi300a", PerNode: 4}}},
			}},
		{Partition: "retired", State: "down", MaxTime: time.Hour, MaxNodes: 4, Nodes: 3, NodeTypes: []NodeType{{Nodes: 3, Cpus: 8, MemMB: 1000}}},
	}
	limits[2].Nodes = 15

	var out bytes.Buffer
	WritePartitionTable(&out, limits, nil)

	want := `PARTITION          AVAIL  NODES  CPUS    MEM  MAXTIME  MAXNODES  GPUS
cpubase_interac    up       702   192   748G    08:00         -  -
cpubackfill*       up       709   192  748G+    24:00         -  -
gpubase_bynode_b4  up         6   112  2000G    72:00         -  h100:8
                              5   112  2000G                     nvidia_h100_80gb_hbm3_3g.40gb:8
                                                                 nvidia_h100_80gb_hbm3_1g.10gb:16
                              4    96   495G                     mi300a:4
retired            down       3     8  1000M    01:00         4  -
`
	if got := stripColor(out.String()); got != want {
		t.Errorf("table mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Node types whose memory is within 10% share a row, at the smallest size with a +.
func TestPartitionTableMergesMemoryClasses(t *testing.T) {
	gpu := []NodeGpu{{Type: "t4", PerNode: 2}}
	limits := []ResourceLimits{
		{Partition: "a", State: "up", NodeTypes: []NodeType{
			{Nodes: 1, Cpus: 64, MemMB: 524072},
			{Nodes: 1, Cpus: 64, MemMB: 523547},
			{Nodes: 203, Cpus: 192, MemMB: 766000},
			{Nodes: 10, Cpus: 192, MemMB: 6144000},
			{Nodes: 2, Cpus: 64, MemMB: 524072, Gpus: gpu},
			{Nodes: 2, Cpus: 64, MemMB: 524072},
		}},
	}
	var out bytes.Buffer
	WritePartitionTable(&out, limits, nil)
	want := `PARTITION  AVAIL  NODES  CPUS    MEM  MAXTIME  GPUS
a          up         4    64  511G+        -  -
                    203   192   748G           -
                     10   192  6000G           -
                      2    64   512G           t4:2
`
	if got := stripColor(out.String()); got != want {
		t.Errorf("table mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// A table whose partitions have no node cap leaves the MAXNODES column out.
// An unlimited time is "inf", and a time the scheduler does not report is "-".
func TestPartitionTableWithoutNodeCap(t *testing.T) {
	limits := []ResourceLimits{
		{Partition: "a", State: "up", MaxTime: time.Hour, NodeTypes: []NodeType{{Nodes: 2, Cpus: 8, MemMB: 16384}}},
		{Partition: "b", State: "up", TimeUnlimited: true, NodeTypes: []NodeType{{Nodes: 2, Cpus: 8, MemMB: 16384}}},
		{Partition: "c", State: "up", NodeTypes: []NodeType{{Nodes: 2, Cpus: 8, MemMB: 16384}}},
	}
	var out bytes.Buffer
	WritePartitionTable(&out, limits, nil)
	want := `PARTITION  AVAIL  NODES  CPUS  MEM  MAXTIME  GPUS
a          up         2     8  16G    01:00  -
b          up         2     8  16G      inf  -
c          up         2     8  16G        -  -
`
	if got := stripColor(out.String()); got != want {
		t.Errorf("table mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

var colorRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripColor(s string) string { return colorRe.ReplaceAllString(s, "") }

func TestPartitionRowsWithoutNodeTypes(t *testing.T) {
	l := ResourceLimits{Partition: "q1", Nodes: 0, MaxCpusPerNode: 32, MaxMemMBPerNode: 65536}
	rows := partitionRows(l, []GpuInfo{{Type: "v100", Total: 4}})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(rows), rows)
	}
	want := []string{"q1", "-", "-", "32", "64G", "-", "-", "v100"}
	for i, w := range want {
		if rows[0][i] != w {
			t.Errorf("column %s = %q, want %q", partitionColumns[i], rows[0][i], w)
		}
	}
}
