package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// gpuUsageLine is a row of sinfoGpuFields: each value padded to its width, then "|".
// A value longer than its width is cut, as sinfo does.
func gpuUsageLine(values ...string) string {
	var b strings.Builder
	for i, f := range sinfoGpuFields {
		v := values[i]
		if len(v) > f.width {
			v = v[:f.width]
		}
		b.WriteString(v)
		b.WriteString(strings.Repeat(" ", f.width-len(v)))
		b.WriteString("|")
	}
	return b.String() + "\n"
}

const migPartition = "b4|up|3-00:00:00|1-infinite|112|2048000|gpu:mig3:8(S:0-3),gpu:mig1:16(S:0-3)|4/0/0/4|0/0/0/448\n"

const migGres = "gpu:mig3:8(S:0-3),gpu:mig1:16(S:0-3)"

func idleOf(limits []ResourceLimits) map[string]string {
	got := map[string]string{}
	for _, l := range limits {
		for _, t := range l.NodeTypes {
			for _, g := range t.Gpus {
				if g.Idle == nil {
					got[l.Partition+":"+g.Type] = "unreported"
				} else {
					got[l.Partition+":"+g.Type] = strconv.Itoa(*g.Idle)
				}
			}
		}
	}
	return got
}

func TestParseSinfoGpuUsage(t *testing.T) {
	rows := gpuUsageLine("b4", "1", "112", "2048000", migGres, "gpu:mig3:4(IDX:4,16,20,28),gpu:mig1:15(IDX:2-3,6-7)", "mixed-") +
		gpuUsageLine("b4", "1", "112", "2048000", migGres, "gpu:mig3:2(IDX:4,12),gpu:mig1:13(IDX:3,10-11)", "mixed") +
		gpuUsageLine("b4", "1", "112", "2048000", migGres, "gpu:mig3:8(IDX:0,4),gpu:mig1:16(IDX:2-3)", "allocated") +
		gpuUsageLine("b4", "2", "112", "2048000", migGres, "gpu:mig3:0(IDX:N/A)", "idle") +
		gpuUsageLine("b4", "1", "112", "2048000", migGres, "(null)", "drained*") +
		gpuUsageLine("b4", "1", "112", "2048000", migGres, "(null)", "down")
	limits, _ := parseSinfoPartitions(migPartition)
	parseSinfoGpuUsage(rows, limits)

	// mig3: 4 + 6 + 0 + 16 free on nodes that take jobs. mig1: 1 + 3 + 0 + 32. A node in use of a type the
	// row does not list has all of it free, and drained and down nodes add none.
	want := map[string]string{"b4:mig3": "26", "b4:mig1": "36"}
	if got := idleOf(limits); !reflect.DeepEqual(got, want) {
		t.Errorf("idle = %v, want %v", got, want)
	}
}

func TestParseSinfoGpuUsageKeepsOnlyReadFigures(t *testing.T) {
	plain := "gpu:h100:8"
	partitions := "a|up|infinite|1-infinite|64|256000|" + plain + "|2/0/0/2|0/0/0/128\n" +
		"b|up|infinite|1-infinite|64|256000|" + plain + "|2/0/0/2|0/0/0/128\n"
	good := gpuUsageLine("a", "2", "64", "256000", plain, "gpu:h100:6(IDX:0-5)", "mixed")
	cut := gpuUsageLine("b", "1", "64", "256000", plain, "gpu:h100:6(IDX:"+strings.Repeat("0,", 600), "mixed")

	tests := []struct {
		name   string
		output string
		want   map[string]string
	}{
		{"a cut row leaves its node type without a figure", good + cut, map[string]string{"a:h100": "4", "b:h100": "unreported"}},
		{"a line that does not split leaves every node type without one", good + "garbage\n", map[string]string{"a:h100": "unreported", "b:h100": "unreported"}},
		{"a node type with no row has no figure", good, map[string]string{"a:h100": "4", "b:h100": "unreported"}},
		{"no rows", "", map[string]string{"a:h100": "unreported", "b:h100": "unreported"}},
	}
	for _, tt := range tests {
		limits, _ := parseSinfoPartitions(partitions)
		parseSinfoGpuUsage(tt.output, limits)
		if got := idleOf(limits); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: idle = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestParseSinfoGpuUsageZeroIsAFigure(t *testing.T) {
	limits, _ := parseSinfoPartitions("a|up|infinite|1-infinite|64|256000|gpu:h100:8|1/0/0/1|0/0/0/64\n")
	parseSinfoGpuUsage(gpuUsageLine("a", "1", "64", "256000", "gpu:h100:8", "gpu:h100:8(IDX:0-7)", "allocated"), limits)
	if idle := limits[0].NodeTypes[0].Gpus[0].Idle; idle == nil || *idle != 0 {
		t.Errorf("idle = %v, want 0", idle)
	}
}

func TestSplitGres(t *testing.T) {
	tests := map[string][]string{
		"gpu:a:2(IDX:0,4),gpu:b:1(IDX:1)": {"gpu:a:2(IDX:0,4)", "gpu:b:1(IDX:1)"},
		"gpu:h100:8(S:0-3)":               {"gpu:h100:8(S:0-3)"},
		"(null)":                          {"(null)"},
		"":                                {""},
		"gpu:a:2(IDX:0,4":                 {"gpu:a:2(IDX:0,4"},
	}
	for in, want := range tests {
		if got := splitGres(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitGres(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSlurmGresReadsGpusInUse(t *testing.T) {
	got := parseSlurmGres("gpu:nvidia_h100_3g.40gb:4(IDX:4,16,20,28),gpu:nvidia_h100_1g.10gb:15(IDX:2-3,6-7)")
	want := []NodeGpu{{Type: "nvidia_h100_3g.40gb", PerNode: 4}, {Type: "nvidia_h100_1g.10gb", PerNode: 15}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gpus = %+v, want %+v", got, want)
	}
}

func TestSlurmNodeTakesJobs(t *testing.T) {
	tests := map[string]bool{
		"idle": true, "mixed": true, "mixed-": true, "allocated": true, "Allocated*": true, "idle~": true,
		"drained": false, "drained*": false, "draining": false, "down": false, "down*": false,
		"maint": false, "reserved": false, "": false,
	}
	for state, want := range tests {
		if got := slurmNodeTakesJobs(state); got != want {
			t.Errorf("slurmNodeTakesJobs(%q) = %v, want %v", state, got, want)
		}
	}
}

// fakeSinfo is a sinfo that answers the partition call with partitions and the GPU usage call with usage,
// and records the arguments of the usage call. exitCode is the status of the usage call.
func fakeSinfo(t *testing.T, partitions, usage string, exitCode int) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	files := map[string]string{"partitions": partitions, "usage": usage}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"*\" -o \"*) cat '" + filepath.Join(dir, "partitions") + "' ;;\n" +
		"*) echo \"$@\" > '" + argsFile + "'; cat '" + filepath.Join(dir, "usage") + "'; exit " + strconv.Itoa(exitCode) + " ;;\n" +
		"esac\n"
	bin = filepath.Join(dir, "sinfo")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func TestGetClusterInfoReadsIdleGpus(t *testing.T) {
	usage := gpuUsageLine("b4", "2", "112", "2048000", migGres, "gpu:mig3:4(IDX:0-3)", "mixed")
	bin, argsFile := fakeSinfo(t, migPartition+"cpu|up|infinite|1-infinite|8|1000|(null)|3/0/0/3|0/0/0/24\n", usage, 0)
	info, err := (&SlurmScheduler{sinfoCommand: bin}).GetClusterInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := idleOf(info.Limits), map[string]string{"b4:mig3": "8", "b4:mig1": "32"}; !reflect.DeepEqual(got, want) {
		t.Errorf("idle = %v, want %v", got, want)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-p b4 ") || strings.Contains(string(args), "cpu") {
		t.Errorf("usage call args = %q, want only the partition with GPUs", args)
	}
	if strings.Contains(string(args), "-N") {
		t.Errorf("usage call args = %q, want no per-node listing", args)
	}
}

func TestGetClusterInfoWithoutGpuPartitionsMakesNoUsageCall(t *testing.T) {
	bin, argsFile := fakeSinfo(t, "cpu|up|infinite|1-infinite|8|1000|(null)|3/0/0/3|0/0/0/24\n", "", 0)
	if _, err := (&SlurmScheduler{sinfoCommand: bin}).GetClusterInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("the usage call ran although no partition has GPUs")
	}
}

func TestGetClusterInfoKeepsPartitionsWhenTheUsageCallFails(t *testing.T) {
	bin, _ := fakeSinfo(t, migPartition, "", 1)
	info, err := (&SlurmScheduler{sinfoCommand: bin}).GetClusterInfo(context.Background())
	if err != nil {
		t.Fatalf("a failed usage call failed the listing: %v", err)
	}
	if got, want := idleOf(info.Limits), map[string]string{"b4:mig3": "unreported", "b4:mig1": "unreported"}; !reflect.DeepEqual(got, want) {
		t.Errorf("idle = %v, want %v", got, want)
	}
}
