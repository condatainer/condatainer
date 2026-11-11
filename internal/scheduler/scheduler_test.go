package scheduler

import (
	"testing"
	"time"
)

func TestSpecDefaultsInitialValues(t *testing.T) {
	defaults := GetSpecDefaults()
	if defaults.CpusPerTask != 1 {
		t.Errorf("CpusPerTask = %d; want 1", defaults.CpusPerTask)
	}
	if defaults.Nodes != 0 {
		t.Errorf("Nodes = %d; want 0", defaults.Nodes)
	}
	if defaults.TasksPerNode != 0 {
		t.Errorf("TasksPerNode = %d; want 0", defaults.TasksPerNode)
	}
	if defaults.MemPerNodeMB != 2048 {
		t.Errorf("MemPerNodeMB = %d; want 2048", defaults.MemPerNodeMB)
	}
	if defaults.Time != 2*time.Hour {
		t.Errorf("Time = %v; want 2h", defaults.Time)
	}
}

func TestEffectiveMaxNodes(t *testing.T) {
	tests := []struct {
		name string
		l    ResourceLimits
		want int
	}{
		{"cap below the partition", ResourceLimits{MaxNodes: 4, Nodes: 10}, 4},
		{"no cap", ResourceLimits{Nodes: 10}, 10},
		{"cap above the partition", ResourceLimits{MaxNodes: 40, Nodes: 10}, 10},
		{"cap only", ResourceLimits{MaxNodes: 4}, 4},
		{"neither", ResourceLimits{}, 0},
	}
	for _, tc := range tests {
		if got := tc.l.EffectiveMaxNodes(); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
