package usage

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/condatainer/condatainer/internal/utils"
)

// fresh resets the process state and captures what Finish prints.
func fresh(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	mu.Lock()
	ctx, kind, printLine, worked, finished, sinks = nil, "", false, false, false, nil
	oldEmit, oldMeasure, oldRank, oldQuiet := emit, measureNow, multiRank, utils.QuietMode
	emit = func(line string) { lines = append(lines, line) }
	measureNow = func() Result { return Result{CPU: 4 * time.Second, PeakMB: 2} }
	multiRank = func() bool { return false }
	utils.QuietMode = false
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		emit, measureNow, multiRank, utils.QuietMode = oldEmit, oldMeasure, oldRank, oldQuiet
		mu.Unlock()
	})
	return &lines
}

func TestLine(t *testing.T) {
	cases := []struct {
		name string
		in   Result
		want string
	}{
		{"all figures", Result{Wall: 57*time.Minute + 21*time.Second, CPU: 4*time.Hour + 21*time.Minute + 49*time.Second, PeakMB: 32 * 1024},
			"Resource usage: wall=00:57:21 cpu=04:21:49 (4.6 cores) peak_mem=32.0 GiB"},
		{"a day or more has days", Result{Wall: 27*time.Hour + 5*time.Minute + 11*time.Second, CPU: time.Hour, PeakMB: 1},
			"Resource usage: wall=1-03:05:11 cpu=01:00:00 (0.0 cores) peak_mem=1.00 MiB"},
		{"under 10 GiB has two decimals", Result{Wall: time.Minute, CPU: time.Minute, PeakMB: 2150},
			"Resource usage: wall=00:01:00 cpu=00:01:00 (1.0 cores) peak_mem=2.10 GiB"},
		{"no figures from the platform", Result{Wall: 90 * time.Second}, "Resource usage: wall=00:01:30"},
	}
	for _, c := range cases {
		if got := Line(c.in); got != c.want {
			t.Errorf("%s: Line = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRecordFollowsTheKey(t *testing.T) {
	for _, kind := range []string{KindCreate, KindRun, KindHelper, "exec", ""} {
		if Record(kind) {
			t.Errorf("Record(%q) with no layer setting the key", kind)
		}
	}
	settingstest.OverrideList(t, "record_usage", KindRun)
	if !Record(KindRun) || Record(KindCreate) {
		t.Error("a named kind records alone")
	}
	settingstest.OverrideList(t, "record_usage", KindAll)
	if !Record(KindRun) || !Record(KindCreate) || Record(KindHelper) || Record("exec") {
		t.Error("all stands for create and run")
	}
	settingstest.OverrideList(t, "record_usage", KindAll, KindNone)
	if Record(KindRun) || Record(KindCreate) {
		t.Error("none turns every kind off")
	}
}

func TestJobFlags(t *testing.T) {
	settingstest.OverrideList(t, "record_usage", KindRun)
	if got := JobFlags(KindRun, false); len(got) != 1 || got[0] != "--usage" {
		t.Errorf("a recorded kind with no typed flag = %q", got)
	}
	if got := JobFlags(KindCreate, true); len(got) != 1 || got[0] != "--usage" {
		t.Errorf("a typed flag reaches the job whatever the key says, got %q", got)
	}
	if got := JobFlags(KindCreate, false); got != nil {
		t.Errorf("an unrecorded kind = %q", got)
	}
}

func TestFinishPrintsOnceAfterWork(t *testing.T) {
	lines := fresh(t)
	Configure(KindRun, true)
	Finish()
	if len(*lines) != 0 {
		t.Fatalf("no work was done, printed %q", *lines)
	}

	lines = fresh(t)
	Configure(KindRun, true)
	Worked()
	Finish()
	Finish()
	if len(*lines) != 1 {
		t.Fatalf("printed %d lines, want 1: %q", len(*lines), *lines)
	}
}

func TestFinishWithoutConfigureOrAsk(t *testing.T) {
	lines := fresh(t)
	Worked()
	Finish()
	if len(*lines) != 0 {
		t.Errorf("a command that never configured printed %q", *lines)
	}

	lines = fresh(t)
	Configure(KindRun, false)
	Worked()
	Finish()
	if len(*lines) != 0 {
		t.Errorf("a kind the key does not name printed %q", *lines)
	}
}

func TestQuietHidesOnlyWhatTheKeyAsked(t *testing.T) {
	settingstest.OverrideList(t, "record_usage", KindRun)
	lines := fresh(t)
	utils.QuietMode = true
	Configure(KindRun, false)
	Worked()
	Finish()
	if len(*lines) != 0 {
		t.Errorf("-q left a line the key asked for: %q", *lines)
	}

	lines = fresh(t)
	utils.QuietMode = true
	Configure(KindRun, true)
	Worked()
	Finish()
	if len(*lines) != 1 {
		t.Errorf("a typed --usage under -q printed %d lines", len(*lines))
	}
}

func TestHelperAlwaysPrints(t *testing.T) {
	lines := fresh(t)
	Configure(KindHelper, false)
	Worked()
	Finish()
	if len(*lines) != 1 {
		t.Errorf("a helper printed %d lines, want 1", len(*lines))
	}
}

func TestCancelPrintsNothingExceptForAHelper(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	lines := fresh(t)
	Watch(cancelled)
	Configure(KindRun, true)
	Worked()
	Finish()
	if len(*lines) != 0 {
		t.Errorf("a cancelled run printed %q", *lines)
	}

	lines = fresh(t)
	Watch(cancelled)
	Configure(KindHelper, false)
	Worked()
	Finish()
	if len(*lines) != 1 {
		t.Errorf("a helper's stop printed %d lines, want 1", len(*lines))
	}
}

func TestMultiRankJobPrintsNothing(t *testing.T) {
	lines := fresh(t)
	multiRank = func() bool { return true }
	Configure(KindRun, true)
	Worked()
	Finish()
	if len(*lines) != 0 {
		t.Errorf("a job with several ranks printed %q", *lines)
	}
}

func TestSinkReceivesTheMeasurement(t *testing.T) {
	lines := fresh(t)
	var got Result
	OnFinish(func(r Result) { got = r })
	Configure(KindHelper, false)
	Worked()
	Finish()
	if len(*lines) != 1 || got.PeakMB != 2 || got.CPU != 4*time.Second || got.Wall <= 0 {
		t.Errorf("lines = %q, result = %+v", *lines, got)
	}
}

// TestHelperProcess is the child of TestMeasureSeesAReapedChild: it holds 200 MiB resident and exits.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("USAGE_TEST_CHILD") != "1" {
		return
	}
	block := make([]byte, 200<<20)
	for i := range block {
		block[i] = 1
	}
	if block[len(block)-1] != 1 {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMeasureSeesAReapedChild(t *testing.T) {
	if _, _, ok := rusage(); !ok {
		t.Skip("getrusage failed")
	}
	before := measure()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "USAGE_TEST_CHILD=1")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	after := measure()
	if after.PeakMB < 200 {
		t.Errorf("peak = %d MB, want at least the child's 200 MiB", after.PeakMB)
	}
	if after.CPU <= before.CPU {
		t.Errorf("CPU %v did not grow past %v after a child ran", after.CPU, before.CPU)
	}
}

func TestHelperPrintsWhateverTheKeySays(t *testing.T) {
	settingstest.OverrideList(t, "record_usage", KindNone)
	lines := fresh(t)
	Configure(KindHelper, false)
	Worked()
	Finish()
	if len(*lines) != 1 {
		t.Errorf("a helper printed %d lines under none, want 1", len(*lines))
	}
}
