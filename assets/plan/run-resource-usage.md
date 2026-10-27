# Plan: resource usage line for jobs

Status: not implemented.

## Goal

A job log ends with the peak memory, CPU time and wall time of the script that ran, measured
with GNU `time` inside the container. A user sizes `#SBATCH --mem` and `--time` from the log
of one finished job instead of from `sacct`, which needs scheduler accounting.

## Current state

- `base.def` installs the `time` package, so `/usr/bin/time` is in every container on the
  `ubuntu24` base.
- `runLocally` (`cmd/run.go`) starts `/bin/bash -c` with `"$0" "$@"` (or `bash "$0" "$@"`)
  inside the container. This is the one place a script starts, for submitted jobs and for
  `--no-submit`.
- The job script footer (`writeJobFooter`, `internal/scheduler/script_helpers.go`) prints job
  ID, elapsed time from `$SECONDS`, completion time and exit code. It has no memory.
- `--debug` prints debug records and keeps the generated job script. It does not run anything
  under `time`.

## Design

- `runLocally` prefixes the script command with
  `/usr/bin/time -f "<format>"`, run inside the container, so the base image supplies it.
- The format is one line on stderr, which the scheduler already writes to the job log:

  ```text
  [usage] peak_rss=31.2G wall=0:57:21 user=4:12:08 sys=0:09:41 cpu=470% exit=0
  ```

- `%M` (kB) is converted to a size, `%e` to `h:mm:ss`, `%U`, `%S`, `%P` and `%x` are printed
  as they are. The exit status of the script is the exit status of `time`, so
  `_EXIT_CODE` in the job footer is unchanged.
- When `/usr/bin/time` does not exist in the container, the script runs unwrapped and one
  line says so.
- A script that is killed (for example by the memory limit) still gets a line, because `time`
  is the parent and reads the child's usage with `wait4`.

## Limits

- `%M` is the largest resident set of any one process in the tree, not their sum. A script
  that runs several large programs at once under-reports. `sacct` `MaxRSS` is the tool for
  that.
- It does not include page cache.
- Shell built-ins and `exec` chains are measured as the shell's own children.

## Flag

- `condatainer run --usage` and a `#CNT --usage` header line turn it on.
- Without either, the command is not wrapped and the log is unchanged.
- `create` runs a build script as a job; `--usage` applies there too, so a recipe's
  `#SBATCH` values can be set from a real build.

## Files

| File | Change |
|---|---|
| `cmd/run.go` | `--usage` flag, `#CNT --usage` parsing, wrap `executionScript` |
| `internal/runtime/exec` | an option that carries the wrapper into the container command |
| `internal/build` | pass `--usage` to the build job's `run` |
| `docs/.../guides/scheduler.mdx` | document the flag and the line |
| CLI help of `run` and `create` | one line for the flag |

## Tests

- The generated command contains the `time` prefix with `--usage` and has none without it.
- The usage line is parsed from a fixed `time` output: size conversion, wall format, and the
  case where `/usr/bin/time` is missing.
- The exit code of a failing script is preserved.

## Open decisions

1. **On by default for submitted jobs?** One extra process per job and one log line; a
   line in every log makes sizing requests routine. Recommended: on for submitted jobs, off
   for interactive `--no-submit` runs, `--no-usage` to turn it off.
2. **Flag name:** `--usage`, `--time`, or `--resources`.
3. **Also print the requested values** (`ReqMem`, `ReqTime`) on the same line, so the log
   shows how close the job came to its limit.
