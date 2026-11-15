package cmd

import (
	"testing"

	"github.com/condatainer/condatainer/internal/runtime/hostrun"
)

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
