package build

import (
	"testing"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
)

// Build jobs take their partition and account from build.*, then scheduler.*, never from a recipe.
func TestSetJobTarget(t *testing.T) {
	override := func(key, value string) {
		restore, err := settings.OverrideValue(key, value)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(restore)
	}
	recipe := func() *scheduler.ScriptSpecs {
		s := &scheduler.ScriptSpecs{}
		s.Control.Partition, s.Control.Account = "recipe", "recipe"
		return s
	}

	override("scheduler.partition", "sched-part")
	override("scheduler.account", "sched-acct")
	s := recipe()
	SetJobTarget(s)
	if s.Control.Partition != "sched-part" || s.Control.Account != "sched-acct" {
		t.Errorf("scheduler fallback: %q %q", s.Control.Partition, s.Control.Account)
	}

	override("build.partition", "build-part")
	override("build.account", "build-acct")
	s = recipe()
	SetJobTarget(s)
	if s.Control.Partition != "build-part" || s.Control.Account != "build-acct" {
		t.Errorf("build config: %q %q", s.Control.Partition, s.Control.Account)
	}
}

// A build job is "cnt-b-" plus its own #NAME:, or plus the whole name/version.
func TestSetJobName(t *testing.T) {
	for _, tc := range []struct{ own, nameVersion, want string }{
		{"", "star/2.7.11b", "cnt-b-star-2.7.11b"},
		{"", "grch38/star/2.7.11b/gencode47-151", "cnt-b-grch38-star-2.7.11b-gencode47-151"},
		{"quant", "star/2.7.11b", "cnt-b-quant"},
		{"cnt-b-quant", "star/2.7.11b", "cnt-b-quant"},
	} {
		s := &scheduler.ScriptSpecs{}
		s.Control.JobName = tc.own
		SetJobName(s, tc.nameVersion)
		if s.Control.JobName != tc.want {
			t.Errorf("own %q, %s: got %q, want %q", tc.own, tc.nameVersion, s.Control.JobName, tc.want)
		}
	}
}
