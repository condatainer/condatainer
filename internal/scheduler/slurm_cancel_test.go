package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withScancel adds a scancel to the fake directory that prints out and exits with rc.
func (f *fakeSlurm) withScancel(t *testing.T, out string, rc int) *fakeSlurm {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "scancel.out"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\necho \"scancel $@\" >> %s/calls\nenv | grep -E '^SCANCEL_' >> %s/env.scancel\ncat %s/scancel.out\nexit %d\n", f.dir, f.dir, f.dir, rc)
	if err := os.WriteFile(filepath.Join(f.dir, "scancel"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func cancel(t *testing.T, f *fakeSlurm, ids ...string) ([]CancelResult, error) {
	t.Helper()
	return f.CancelJobs(context.Background(), ids)
}

func TestCancelJobsMakesOneCallForAllIDs(t *testing.T) {
	f := newFakeSlurm(t, "", 0, "", 0).withScancel(t, "", 0)
	results, err := cancel(t, f, "5", "6", "7_2")
	if err != nil {
		t.Fatal(err)
	}
	if f.count("scancel") != 1 || f.calls()[0] != "scancel --me 5 6 7_2" {
		t.Errorf("calls = %v, want one scancel --me with every ID", f.calls())
	}
	for i, id := range []string{"5", "6", "7_2"} {
		if results[i] != (CancelResult{ID: id, Outcome: CancelSent}) {
			t.Errorf("result %d = %+v, want %s sent", i, results[i], id)
		}
	}
}

func TestCancelJobsReportsTheIDScancelRefused(t *testing.T) {
	out := "scancel: error: Kill job error on job id 6: Invalid job id specified\n"
	results, err := cancel(t, newFakeSlurm(t, "", 0, "", 0).withScancel(t, out, 1), "5", "6", "7")
	if err != nil {
		t.Fatal(err)
	}
	want := []CancelResult{{"5", CancelSent, ""}, {"6", CancelError, "Invalid job id specified"}, {"7", CancelSent, ""}}
	for i := range want {
		if results[i] != want[i] {
			t.Errorf("result %d = %+v, want %+v", i, results[i], want[i])
		}
	}
}

func TestCancelJobsFailedCallIsAnError(t *testing.T) {
	for name, out := range map[string]string{
		"no error line":       "",
		"a line of its own":   "scancel: error: Unable to contact slurm controller\n",
		"an ID not asked for": "scancel: error: Kill job error on job id 99: Invalid job id specified\n",
	} {
		_, err := cancel(t, newFakeSlurm(t, "", 0, "", 0).withScancel(t, out, 1), "5")
		if err == nil {
			t.Errorf("%s: scancel failed and no ID was refused by name, want an error", name)
		}
	}
	_, err := cancel(t, newFakeSlurm(t, "", 0, "", 0).withScancel(t, "scancel: error: Kill job error on job id 99: Invalid job id specified\n", 0), "5")
	if err == nil {
		t.Error("an error for an ID that was not asked for must not leave 5 reported as sent")
	}
}

func TestCancelJobsRunsWithoutUserDefaults(t *testing.T) {
	t.Setenv("SCANCEL_PARTITION", "elsewhere")
	f := newFakeSlurm(t, "", 0, "", 0).withScancel(t, "", 0)
	if _, err := cancel(t, f, "5"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(f.dir, "env.scancel"))
	if strings.Contains(string(got), "SCANCEL_") {
		t.Errorf("scancel saw the user's defaults: %q", got)
	}
}

func TestCancelJobsWithNoIDsCallsNothing(t *testing.T) {
	f := newFakeSlurm(t, "", 0, "", 0).withScancel(t, "", 0)
	if results, err := cancel(t, f); err != nil || results != nil {
		t.Errorf("results = %v, err = %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "calls")); err == nil {
		t.Error("no ID means no call")
	}
}

func TestParseScancelErrors(t *testing.T) {
	refused, other := parseScancelErrors("scancel: error: Kill job error on job id 12_3: Invalid job id specified\nscancel: error: Invalid job id abc\n\n")
	if refused["12_3"] != "Invalid job id specified" || len(refused) != 1 {
		t.Errorf("refused = %v", refused)
	}
	if len(other) != 1 || !strings.Contains(other[0], "Invalid job id abc") {
		t.Errorf("other = %v", other)
	}
}
