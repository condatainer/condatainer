package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// cancelSetup says how the command is run: -y, a terminal, and what the user answers.
type cancelSetup struct {
	yes      bool
	terminal bool
	answer   bool
	asked    []string
}

// runCancel runs `scheduler cancel` against sched and returns what it printed.
func runCancel(t *testing.T, sched scheduler.Scheduler, setup *cancelSetup, args ...string) (string, error) {
	t.Helper()
	scheduler.SetActiveScheduler(sched)
	oldYes, oldTerm, oldConfirm := utils.YesMode, haveTerminal, confirmCancel
	utils.YesMode = setup.yes
	haveTerminal = func() bool { return setup.terminal }
	confirmCancel = func(_ context.Context, _ io.Writer, prompt string) bool {
		setup.asked = append(setup.asked, prompt)
		return setup.answer
	}
	schedulerCancelJSON = false
	t.Cleanup(func() {
		scheduler.ClearActiveScheduler()
		utils.YesMode, haveTerminal, confirmCancel = oldYes, oldTerm, oldConfirm
		schedulerCancelJSON = false
	})

	var rest []string
	for _, a := range args {
		if a == "--json" {
			schedulerCancelJSON = true
		} else {
			rest = append(rest, a)
		}
	}
	c := schedulerCancelCmd
	c.SetContext(context.Background())
	var out bytes.Buffer
	c.SetOut(&out)
	err := runSchedulerCancel(c, rest)
	return out.String(), err
}

func TestCancelByIDCancelsWithoutAsking(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	setup := &cancelSetup{}
	out, err := runCancel(t, f, setup, "21", "9")
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.asked) != 0 {
		t.Errorf("asked %v: naming the job is the confirmation", setup.asked)
	}
	if len(f.cancelled) != 1 || strings.Join(f.cancelled[0], " ") != "21 9" && strings.Join(f.cancelled[0], " ") != "9 21" {
		t.Errorf("cancel calls = %v, want one with both IDs", f.cancelled)
	}
	if !strings.HasPrefix(out, "JOBID") || !strings.Contains(out, "Cancelled: 9, 21") {
		t.Errorf("output:\n%s", out)
	}
}

func TestCancelListsTheJobsBeforeCancellingThem(t *testing.T) {
	out, _ := runCancel(t, &fakeJobScheduler{jobs: fakeQueue()}, &cancelSetup{}, "21")
	if !strings.Contains(out, "align_sample") || strings.Index(out, "align_sample") > strings.Index(out, "Cancelled:") {
		t.Errorf("the table comes first:\n%s", out)
	}
}

func TestCancelAnArrayIDCancelsTheArrayAndATaskIDOneTask(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	if _, err := runCancel(t, f, &cancelSetup{}, "30"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.cancelled[0], " ") != "30" {
		t.Errorf("sent %v, want the array ID alone", f.cancelled[0])
	}
	f = &fakeJobScheduler{jobs: fakeQueue()}
	out, err := runCancel(t, f, &cancelSetup{}, "30_2")
	if err != nil || strings.Join(f.cancelled[0], " ") != "30_2" || !strings.Contains(out, "Cancelled: 30_2") {
		t.Errorf("sent %v, err = %v:\n%s", f.cancelled, err, out)
	}
	f = &fakeJobScheduler{jobs: fakeQueue()}
	if _, err := runCancel(t, f, &cancelSetup{}, "30", "30_2"); err != nil || strings.Join(f.cancelled[0], " ") != "30" {
		t.Errorf("sent %v, err = %v: the array already covers its task", f.cancelled, err)
	}
}

func TestCancelAnIDTheListingDoesNotHaveIsNeverSent(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	out, err := runCancel(t, f, &cancelSetup{}, "21", "999")
	if !errors.Is(err, errSomeNotCancelled) {
		t.Errorf("err = %v, want the exit status of a job not found", err)
	}
	if len(f.cancelled) != 1 || strings.Join(f.cancelled[0], " ") != "21" {
		t.Errorf("sent %v: an ID the listing did not find is not passed to the scheduler", f.cancelled)
	}
	if !strings.Contains(out, "Cancelled: 21") || !strings.Contains(out, "Not found: 999") {
		t.Errorf("output:\n%s", out)
	}
}

func TestCancelOnlyUnknownIDsCallsNothing(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	out, err := runCancel(t, f, &cancelSetup{}, "999")
	if !errors.Is(err, errSomeNotCancelled) || len(f.cancelled) != 0 || !strings.Contains(out, "Not found: 999") {
		t.Errorf("err = %v, sent %v:\n%s", err, f.cancelled, out)
	}
}

func TestCancelRefusalIsReportedAndSetsTheExitStatus(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue(), cancel: func(ids []string) ([]scheduler.CancelResult, error) {
		return []scheduler.CancelResult{{ID: "21", Outcome: scheduler.CancelError, Message: "Access denied"}, {ID: "9", Outcome: scheduler.CancelSent}}, nil
	}}
	out, err := runCancel(t, f, &cancelSetup{}, "21", "9")
	if !errors.Is(err, errSomeNotCancelled) {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(out, "Cancelled: 9") || strings.Contains(out, "Cancelled: 9, 21") || strings.Contains(out, "Not found") {
		t.Errorf("output:\n%s", out)
	}
}

func TestCancelFailedCallIsAnError(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue(), cancel: func([]string) ([]scheduler.CancelResult, error) {
		return nil, errors.New("Unable to contact slurm controller")
	}}
	out, err := runCancel(t, f, &cancelSetup{}, "21")
	if err == nil || !strings.Contains(err.Error(), "Unable to contact") || strings.Contains(out, "Cancelled:") {
		t.Errorf("err = %v:\n%s", err, out)
	}
}

func TestCancelByNameAsksFirst(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	setup := &cancelSetup{terminal: true, answer: true}
	out, err := runCancel(t, f, setup, "align")
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.asked) != 1 || setup.asked[0] != "Cancel these 2 jobs? [y/N]: " {
		t.Errorf("asked %q", setup.asked)
	}
	if strings.Join(f.cancelled[0], " ") != "9 21" || !strings.Contains(out, "Cancelled: 9, 21") {
		t.Errorf("sent %v:\n%s", f.cancelled, out)
	}
	if !strings.Contains(out, "align_sample") {
		t.Errorf("the jobs are listed before the question:\n%s", out)
	}
}

func TestCancelByNameAnsweredNoCancelsNothing(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	_, err := runCancel(t, f, &cancelSetup{terminal: true, answer: false}, "align")
	if err != nil || len(f.cancelled) != 0 {
		t.Errorf("err = %v, sent %v", err, f.cancelled)
	}
}

func TestCancelByNameWithYesDoesNotAsk(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	setup := &cancelSetup{yes: true}
	if _, err := runCancel(t, f, setup, "index"); err != nil {
		t.Fatal(err)
	}
	if len(setup.asked) != 0 || strings.Join(f.cancelled[0], " ") != "9" {
		t.Errorf("asked %v, sent %v", setup.asked, f.cancelled)
	}
}

func TestCancelByNameWithoutATerminalCancelsNothing(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	setup := &cancelSetup{}
	_, err := runCancel(t, f, setup, "align")
	if err == nil || !strings.Contains(err.Error(), "-y") || len(f.cancelled) != 0 || len(setup.asked) != 0 {
		t.Errorf("err = %v, sent %v, asked %v", err, f.cancelled, setup.asked)
	}
}

func TestCancelByNameFoldsAnArrayIntoItsID(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	if _, err := runCancel(t, f, &cancelSetup{yes: true}, "count"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.cancelled[0], " ") != "30" {
		t.Errorf("sent %v, want the array ID, not a call per task", f.cancelled)
	}
}

func TestCancelByNameMatchingNothingCancelsNothing(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	out, err := runCancel(t, f, &cancelSetup{terminal: true}, "nomatch")
	if err != nil || len(f.cancelled) != 0 || !strings.HasPrefix(out, "JOBID") {
		t.Errorf("err = %v, sent %v:\n%s", err, f.cancelled, out)
	}
}

func TestCancelRefusesIDsMixedWithNamesAndNoArguments(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	if _, err := runCancel(t, f, &cancelSetup{yes: true}, "21", "align"); !errors.Is(err, errMixedJobArgs) {
		t.Errorf("err = %v", err)
	}
	if len(f.cancelled) != 0 || f.calls != 0 {
		t.Error("a refused call must not reach the scheduler")
	}
	if err := schedulerCancelCmd.Args(schedulerCancelCmd, nil); err == nil {
		t.Error("a bare cancel must be an error, never cancel everything")
	}
}

func TestCancelOnASchedulerWithoutCancel(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	_, err := runCancel(t, listsOnly{inner: f}, &cancelSetup{}, "21")
	if err == nil || err.Error() != "scheduler pbs is not supported" {
		t.Errorf("err = %v", err)
	}
}

func TestCancelJSONOfAByNamePreviewCancelsNothing(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	out, err := runCancel(t, f, &cancelSetup{}, "--json", "align")
	if err != nil || len(f.cancelled) != 0 {
		t.Fatalf("err = %v, sent %v", err, f.cancelled)
	}
	jobs := jsonJobs(t, out)
	if len(jobs) != 2 {
		t.Fatalf("%d records, want the 2 jobs it would cancel", len(jobs))
	}
	for _, j := range jobs {
		if _, ok := j["cancel"]; ok {
			t.Errorf("a preview has no outcome: %v", j)
		}
	}
	if jobs[0]["state"] != "pending" || jobs[1]["state"] != "running" {
		t.Errorf("records show the state before the cancel: %v", jobs)
	}
}

func TestCancelJSONWithYesReportsEachJob(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue(), cancel: func(ids []string) ([]scheduler.CancelResult, error) {
		return []scheduler.CancelResult{{ID: "21", Outcome: scheduler.CancelSent}, {ID: "9", Outcome: scheduler.CancelError, Message: "Access denied"}}, nil
	}}
	out, err := runCancel(t, f, &cancelSetup{yes: true}, "--json", "21", "9", "999")
	if !errors.Is(err, errSomeNotCancelled) {
		t.Errorf("err = %v", err)
	}
	byID := map[string]map[string]any{}
	for _, j := range jsonJobs(t, out) {
		byID[j["id"].(string)] = j
	}
	if byID["21"]["cancel"] != "sent" || byID["21"]["state"] != "running" {
		t.Errorf("21 = %v: sent, with the state it had", byID["21"])
	}
	if byID["9"]["cancel"] != "error" || byID["9"]["error"] != "Access denied" {
		t.Errorf("9 = %v", byID["9"])
	}
	if byID["999"]["cancel"] != "notfound" || byID["999"]["state"] != "notfound" {
		t.Errorf("999 = %v", byID["999"])
	}
}

func TestCancelJSONByIDCancelsWithoutYes(t *testing.T) {
	f := &fakeJobScheduler{jobs: fakeQueue()}
	out, err := runCancel(t, f, &cancelSetup{}, "--json", "21")
	if err != nil || len(f.cancelled) != 1 {
		t.Fatalf("err = %v, sent %v", err, f.cancelled)
	}
	if jsonJobs(t, out)[0]["cancel"] != "sent" {
		t.Errorf("out = %s", out)
	}
}
