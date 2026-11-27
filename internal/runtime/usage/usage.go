// Package usage prints what a command's process used: wall time, CPU time and peak memory.
package usage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/condatainer/condatainer/internal/utils/units"
)

// The job kinds record_usage names, and the launch kind of a helper.
const (
	KindCreate = "create"
	KindRun    = "run"
	KindHelper = "helper"
	KindAll    = "all"
	KindNone   = "none"
)

// Flag is the command-line flag that turns the line on for one command.
const Flag = "usage"

var keyRecord = settings.List("record_usage",
	settings.MergeLists(), settings.Order(9),
	settings.Validate(validateKind),
	settings.Accepts("a list of create, run, all or none"),
	settings.Suggest(KindCreate, KindRun, KindAll, KindNone),
	settings.Help("Job kinds that end with a line of the wall time, CPU time and peak memory they used: create (includes project restore), run or all. None in any layer turns it off. A helper always records."))

func validateKind(kind string) error {
	switch kind {
	case KindCreate, KindRun, KindAll, KindNone:
		return nil
	}
	return fmt.Errorf("%q is not a job kind: use create, run, all or none", kind)
}

// Record reports whether record_usage names kind.
//   - All stands for create and run. None in any layer turns every kind off.
//   - Any other kind, such as a helper or an exec, is never named.
func Record(kind string) bool {
	if kind != KindCreate && kind != KindRun {
		return false
	}
	kinds := keyRecord.Get()
	if slices.Contains(kinds, KindNone) {
		return false
	}
	return slices.Contains(kinds, kind) || slices.Contains(kinds, KindAll)
}

// JobFlags is the flag a submitted job of kind runs with: when the user typed it, or record_usage names the kind.
func JobFlags(kind string, typed bool) []string {
	if !typed && !Record(kind) {
		return nil
	}
	return []string{"--" + Flag}
}

// Result is what a process used.
type Result struct {
	Wall time.Duration
	CPU  time.Duration
	// PeakMB is the largest resident set of the process or of any child it reaped, in whole megabytes rounded up. Zero when no figure was read.
	PeakMB int64
}

// started is the start of the process, so the wall time and the CPU time cover the same span.
var started = time.Now()

var (
	mu         sync.Mutex
	ctx        context.Context
	kind       string
	printLine  bool
	worked     bool
	finished   bool
	sinks      []func(Result)
	emit       = func(line string) { utils.PrintRecord("%s", line) }
	measureNow = measure
	multiRank  = scheduler.InMultiRankJob
)

// Watch gives the package the context a signal cancels. A cancelled run prints nothing, except a helper, which a stop ends.
func Watch(c context.Context) {
	mu.Lock()
	ctx = c
	mu.Unlock()
}

// Configure says which kind of job this process is and whether the user typed --usage.
//   - A helper always prints. Any other kind prints for a typed flag, or when record_usage names it and the command is not quiet.
func Configure(jobKind string, explicit bool) {
	mu.Lock()
	defer mu.Unlock()
	kind = jobKind
	printLine = jobKind == KindHelper || explicit || (Record(jobKind) && !utils.QuietMode)
}

// Worked says this process did local work: it ran a script, built an overlay or pulled one.
func Worked() {
	mu.Lock()
	worked = true
	mu.Unlock()
}

// OnFinish adds fn to what Finish does with the measurement, after the line is printed.
func OnFinish(fn func(Result)) {
	mu.Lock()
	sinks = append(sinks, fn)
	mu.Unlock()
}

// Finish prints the line once, when Configure asked for it and Worked was called.
//   - A job with more than one rank prints nothing.
//   - A cancelled run prints nothing, except a helper.
func Finish() {
	mu.Lock()
	if finished {
		mu.Unlock()
		return
	}
	finished = true
	cancelled := ctx != nil && ctx.Err() != nil
	run := printLine && worked && !(cancelled && kind != KindHelper)
	fns := slices.Clone(sinks)
	mu.Unlock()

	if !run || multiRank() {
		return
	}
	result := measureNow()
	result.Wall = time.Since(started)
	emit(Line(result))
	for _, fn := range fns {
		fn(result)
	}
}

// Line is the text of the usage line, without its prefix.
//   - It says what the process used. The cores figure is CPU time over wall time.
//   - A platform with no usage figures gives the wall time alone.
func Line(r Result) string {
	parts := []string{"wall=" + units.FormatTimeDetail(r.Wall)}
	if r.CPU > 0 || r.PeakMB > 0 {
		cpu := "cpu=" + units.FormatTimeDetail(r.CPU)
		if r.Wall > 0 {
			cpu += fmt.Sprintf(" (%.1f cores)", r.CPU.Seconds()/r.Wall.Seconds())
		}
		parts = append(parts, cpu, "peak_mem="+units.FormatMemoryDetail(r.PeakMB))
	}
	return "Resource usage: " + strings.Join(parts, " ")
}

// measure reads the usage of this process and of every child it reaped.
//   - CPU time is the sum of the two. Peak memory is the larger of the two peaks, since the processes ran together, rounded up to whole megabytes.
func measure() Result {
	self, children, ok := rusage()
	if !ok {
		return Result{}
	}
	return Result{
		CPU:    self.cpu + children.cpu,
		PeakMB: (max(self.peakKB, children.peakKB) + 1023) / 1024,
	}
}

type figures struct {
	cpu    time.Duration
	peakKB int64
}
