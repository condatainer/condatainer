package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/scheduler"
)

// runInfo runs `scheduler info` against a fake scheduler inside a scheduled job. It returns what went to the document
// writer and what went to standard output.
func runInfo(t *testing.T, asJSON bool) (doc, stdout string) {
	t.Helper()
	t.Setenv("SLURM_JOB_ID", "1")
	scheduler.SetActiveScheduler(partitionLister{})
	t.Cleanup(scheduler.ClearActiveScheduler)
	schedulerInfoJSON = asJSON
	t.Cleanup(func() { schedulerInfoJSON = false })

	c := schedulerInfoCmd
	c.SetContext(context.Background())
	var out bytes.Buffer
	c.SetOut(&out)

	oldStdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	runErr := runSchedulerInfo(c, nil)
	write.Close()
	os.Stdout = oldStdout
	text, _ := io.ReadAll(read)
	if runErr != nil {
		t.Fatal(runErr)
	}
	return out.String(), string(text)
}

// With no scheduler found the document has a scheduler whose status says so, and the problem.
func TestSchedulerInfoJSONWithNoScheduler(t *testing.T) {
	clearJobEnv(t)
	scheduler.ClearActiveScheduler()
	t.Setenv("PATH", t.TempDir())
	schedulerInfoJSON = true
	t.Cleanup(func() { schedulerInfoJSON = false })

	c := schedulerInfoCmd
	c.SetContext(context.Background())
	var out bytes.Buffer
	c.SetOut(&out)
	if err := runSchedulerInfo(c, nil); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Scheduler map[string]any `json:"scheduler"`
		Problems  []struct {
			Message string `json:"message"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(doc.Scheduler) != 1 || doc.Scheduler["status"] != "not_found" || len(doc.Problems) != 1 {
		t.Errorf("document = %+v, want only the status not_found, and one problem", doc)
	}
}

// Inside a scheduled job the scheduler is still shown, and the status says that submission is off.
func TestSchedulerInfoInsideAJobShowsTheScheduler(t *testing.T) {
	_, text := runInfo(t, false)
	for _, want := range []string{"Type:      FAKE", "Binary:    /fake/bin/fsub", "Version:   1.0", "Submission disabled (inside job)"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Unavailable") {
		t.Errorf("output calls the scheduler unavailable:\n%s", text)
	}
}

func TestSchedulerInfoJSONInsideAJob(t *testing.T) {
	doc, _ := runInfo(t, true)
	var got struct {
		Scheduler struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"scheduler"`
	}
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	if got.Scheduler.Type != "fake" || got.Scheduler.Status != "inside_job" {
		t.Errorf("scheduler = %+v, want fake and inside_job", got.Scheduler)
	}
}
