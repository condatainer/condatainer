package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/runtime/hostrun"
	"github.com/condatainer/condatainer/internal/scheduler"
)

// allocationStub is an active scheduler whose job has the given resources.
type allocationStub struct {
	slurmStub
	res *scheduler.ResourceSpec
}

// GetJobResources reports the resources only inside a job, as a real scheduler does.
func (a allocationStub) GetJobResources() *scheduler.ResourceSpec {
	if !scheduler.IsInsideJob() {
		return nil
	}
	return a.res
}

// A helper's wrapper names itself. Any other exec, whatever it is told, is a typed exec.
func TestExecHostRunKind(t *testing.T) {
	old := execLaunchKind
	t.Cleanup(func() { execLaunchKind = old })
	for _, tc := range []struct{ flag, want string }{
		{"", hostrun.KindExec},
		{"helper", hostrun.KindHelper},
		{"run", hostrun.KindExec},
		{"anything", hostrun.KindExec},
	} {
		execLaunchKind = tc.flag
		if got := execHostRunKind(); got != tc.want {
			t.Errorf("--launch-kind %q: kind %q, want %q", tc.flag, got, tc.want)
		}
	}
}

// A shell opened in a one-task job gets NCPU and nothing else. Any other job, and no job, gets none.
func TestJobCpuEnv(t *testing.T) {
	allocation := &scheduler.ResourceSpec{CpusPerTask: 4, MemPerNodeMB: 16384}
	for _, tc := range []struct {
		name  string
		job   bool
		tasks string
		set   map[string]string
		want  map[string]string // nil for no variable at all
	}{
		{"no job", false, "", nil, nil},
		{"a one-task job", true, "1", nil, map[string]string{"NCPU": "4"}},
		{"a value of the caller's is replaced", true, "1", map[string]string{"NCPU": "2"}, map[string]string{"NCPU": "4"}},
		{"a job with more than one rank", true, "4", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPlace(t, tc.job, false)
			if tc.job {
				t.Setenv("SLURM_NTASKS", tc.tasks)
			}
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			scheduler.SetActiveScheduler(allocationStub{res: allocation})
			t.Cleanup(scheduler.ClearActiveScheduler)

			got := envMap(jobCpuEnv())
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}
