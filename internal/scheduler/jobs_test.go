package scheduler

import (
	"slices"
	"strconv"
	"testing"
	"time"
)

func task(array, id string, state JobState) JobInfo {
	return JobInfo{ID: array + "_" + id, ArrayID: array, TaskID: id, Name: "count", State: state, Cpus: 4, NodeList: []string{"n1"}, NodeCount: 1}
}

func TestGroupArrayTasks(t *testing.T) {
	jobs := []JobInfo{
		task("9", "3", JobPending), task("9", "10", JobRunning), task("9", "2", JobRunning), task("9", "4", JobPending),
		{ID: "7", Name: "plain", State: JobRunning},
	}
	got := GroupArrayTasks(jobs, false)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want the array and the plain job", len(got))
	}
	array := got[0]
	if array.ID != "9" || array.ArrayID != "9" || array.IsTask() || !array.IsArray() {
		t.Errorf("array row = %+v, want ID 9, an array and not a task", array)
	}
	if array.State != JobRunning || array.Cpus != 4 {
		t.Errorf("state = %q, cpus = %d: running while any task runs, per-task figures kept", array.State, array.Cpus)
	}
	if array.NodeList != nil || array.NodeCount != 0 {
		t.Errorf("an array has no node: %v, %d", array.NodeList, array.NodeCount)
	}
	if !slices.Equal(array.Tasks.Running, []string{"9_2", "9_10"}) || !slices.Equal(array.Tasks.Pending, []string{"9_3", "9_4"}) {
		t.Errorf("tasks = %+v, want the IDs in task order", array.Tasks)
	}
	if got[1].ID != "7" || got[1].IsArray() {
		t.Errorf("plain job = %+v, want it unchanged", got[1])
	}
}

func TestGroupArrayTasksAllPendingIsPending(t *testing.T) {
	got := GroupArrayTasks([]JobInfo{task("9", "1", JobPending), task("9", "2", JobPending)}, false)
	if len(got) != 1 || got[0].State != JobPending || len(got[0].Tasks.Running) != 0 {
		t.Errorf("got %+v, want one pending array with no running task", got)
	}
}

func TestSortJobsByNumber(t *testing.T) {
	jobs := []JobInfo{{ID: "10"}, {ID: "9"}, {ID: "9_10"}, {ID: "9_2"}, {ID: "x1"}, {ID: "100"}}
	SortJobs(jobs)
	var ids []string
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	if want := []string{"9", "9_2", "9_10", "10", "100", "x1"}; !slices.Equal(ids, want) {
		t.Errorf("order = %v, want %v", ids, want)
	}
}

func TestArrayStateOfEndedTasks(t *testing.T) {
	tests := []struct {
		name   string
		states []JobState
		want   JobState
	}{
		{"all succeeded", []JobState{JobSuccess, JobSuccess}, JobSuccess},
		{"one failed", []JobState{JobSuccess, JobFailed, JobCancelled}, JobFailed},
		{"one cancelled", []JobState{JobSuccess, JobCancelled}, JobCancelled},
		{"one could not be read", []JobState{JobSuccess, JobUnknown}, JobUnknown},
		{"a task still pending", []JobState{JobFailed, JobPending}, JobPending},
		{"a task running", []JobState{JobFailed, JobPending, JobRunning}, JobRunning},
	}
	for _, tt := range tests {
		var jobs []JobInfo
		for i, s := range tt.states {
			jobs = append(jobs, task("9", strconv.Itoa(i+1), s))
		}
		if got := GroupArrayTasks(jobs, true)[0].State; got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestArrayTimesNeedEveryTask(t *testing.T) {
	submit := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	mk := func(id string, state JobState, startMin, endMin int) JobInfo {
		j := task("9", id, state)
		j.SubmitTime = submit
		if startMin > 0 {
			j.StartTime = submit.Add(time.Duration(startMin) * time.Minute)
		}
		if endMin > 0 {
			j.EndTime = submit.Add(time.Duration(endMin) * time.Minute)
		}
		return j
	}
	jobs := []JobInfo{mk("1", JobSuccess, 2, 10), mk("2", JobFailed, 1, 30), mk("3", JobSuccess, 3, 20)}

	listing := GroupArrayTasks(jobs, false)[0]
	if !listing.StartTime.IsZero() || !listing.EndTime.IsZero() || listing.Source != SourceQueue {
		t.Errorf("a listing sees part of the array: %+v", listing)
	}
	done := GroupArrayTasks(jobs, true)[0]
	if done.StartTime != submit.Add(time.Minute) || done.EndTime != submit.Add(30*time.Minute) || done.Source != SourceHistory {
		t.Errorf("start = %v, end = %v: the first start and the last end", done.StartTime, done.EndTime)
	}
	open := append(jobs, mk("4", JobRunning, 5, 0))
	if got := GroupArrayTasks(open, true)[0]; !got.EndTime.IsZero() || got.StartTime != submit.Add(time.Minute) {
		t.Errorf("a task still runs, so the array has no end: %+v", got)
	}
}
