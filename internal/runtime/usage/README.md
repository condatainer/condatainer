# internal/runtime/usage

The line a process prints about what it used: wall time, CPU time and peak memory.

## Decisions

- The measurement is the condatainer process itself, taken as it exits.
  - Nothing runs inside the container, so the base image does not matter.
  - A pull, a build and a script are measured the same way.
- CPU time is the process plus every child it reaped. Peak memory is the larger of the two peaks, since the processes ran together.
  - The peak is the largest single process. A grandchild counts only when its parent reaped it.
- The start time is set when the package initializes, so wall time and CPU time cover the same span.
- `Configure` and `Worked` are separate calls.
  - The command knows its kind and whether the flag was typed.
  - The code that does the work, which may be in another package, knows that work happened.
  - A process that only submits jobs never calls `Worked`, so it prints nothing.
- `Finish` runs on every way out.
  - `os.Exit` skips deferred calls, so `cmd` ends through one `exit` helper.
  - A test fails on a bare `os.Exit` in the files that run work.
- A run that does not finish prints nothing.
  - A cancelled context means Ctrl+C or the scheduler's stop, and a partial peak would read as a final one.
  - A helper is the exception. A stop is how a service ends, so its cancel is its end.
- A job with more than one rank prints nothing. Each rank would print its own line, and a rank number needs a variable per MPI launcher.
- A helper always records. `record_usage` names `create` and `run` only.
- The submitting command decides for a job, and tells it with `--usage`.
  - The job never reads `record_usage`, so a setting made only in the submitting shell still applies.
  - The flag is explicit in the job, so `-q` there does not hide the line.
- A helper's usage is written as an event before the wrapper writes `done`, so the run's history record finds it.
- The line goes to stderr with the `[CNT]` prefix, ignoring `-q`. The setting and the flag are the request, and `-q` only hides what the setting asked for.
- A failed `getrusage` call gives no figures, and the line then has the wall time alone.

## Limits

- It does not include page cache.
- A process that is never waited for, such as a daemon left running, is not counted.
- Mount helpers that apptainer starts count toward the peak if apptainer reaps them.
- A hard kill, including a memory-limit kill of the whole job, prints nothing.
