# SLURM is settled, other schedulers next

Status: SLURM is implemented and checked on two real clusters.
The feature is not promoted. It is under testing, and the user docs do not mention it.
PBS, LSF and HTCondor have no `jobs`, job lookup or `cancel`. None has run on a real cluster.

This plan records what SLURM settled and what each other scheduler needs.
Every line stays under 100 columns, so a terminal shows it without wrapping.

## SLURM: done

### Commands of condatainer

- `scheduler info`, `scheduler partitions [--cpu|--gpu]`.
- `scheduler jobs [<id>...|<name>...] [--tasks] [--json]`: a table, or one job in full.
- `scheduler cancel <id>...|<name>... [-y] [--json]`.
- A job of several ranks shows its layout: `2n x 4r x 2c`, or `8r x 2c` if the scheduler placed it.
  - Nodes and ranks per node show only when the user fixed the ranks per node.

### Commands SLURM runs, and how many

| condatainer command | SLURM calls |
|---|---|
| `partitions` | one `sinfo -e -o <fields> --noheader` |
| `jobs` | one `squeue --me -r -h -O <fixed-width fields>` |
| `jobs <id>...` | one `squeue --me -r -h -t all -j <ids> --expand-patterns`, then one `sacct` |
| `cancel` | one `squeue --me` listing, then one `scancel --me <ids>` |

- The `sacct` call is `sacct --array -n -P -j <ids> -o <fields>`.
- It runs only for IDs `squeue` did not return, IDs it returned as ended, and arrays.
- A job that waits or runs never reaches `sacct`.
- Nothing loops. `scontrol` is never called.

### Code

- Neutral model in `internal/scheduler/jobs.go`:
  - `JobInfo`, `JobState`, `JobLister`, `JobCanceller`, `GroupArrayTasks`, `SortJobs`.
- The table (`table.go`), the detail block (`jobs_detail.go`), the report types
  (`internal/report/scheduler.go`) and the commands use only that model.
- SLURM side: `slurm_jobs.go` (`squeue`), `slurm_lookup.go` (`squeue -t all`, `sacct`),
  `slurm_cancel.go` (`scancel`).
- Tested on fixed output, on fake `squeue`, `sacct` and `scancel` scripts, and on a fake lister.
- Run on two real SLURM 25.11 clusters.
  - The built binary ran on one login node.
  - Probe scripts and the real output fed through the parsers covered the other.

### Partitions

- A job goes to one partition, and its CPUs, memory, time and GPUs must fit one node type of it.
  - So `partitions` prints partitions and aggregates nothing across them.
  - `-p` is dropped. `--cpu` and `--gpu` filter which partitions are listed.
- A partition holds a list of node types, since a partition here is a time tier over mixed hardware.
- The table never caps its rows. Hundreds of machines of one shape are one row.
- `sinfo -e` is always on. Without it the larger nodes of a mixed group are hidden.
- The table joins types with the same CPUs and GPUs whose memory is within 10%.
  - The JSON and a partition's largest memory use the exact types.
- The table shows the total nodes.
  - `nodes_up` in the JSON counts nodes that are allocated or idle, not down or drained.
- A node type's CPUs are the node's total. A site may reserve some, so a fit can still be refused.
- No node listing. Per-node diagnosis stays with the scheduler's own tools.
- A per-job cap (a QOS, `resources_max`, `MEMLIMIT`) limits a job, not a node. It is not modelled.
- `jobs` and `cancel` are SLURM only. Others print `scheduler <type> is not supported`.
- `cancel` asks before it cancels jobs a name picked.

### Terminal output

- Help lines stay under 100 columns, with short bullets and no hard-wrapped sentences.
- Help does not repeat what the flag list says. Example comments may.
- A table fits the terminal width. The free-text columns (`NAME`, `NOTE`) shrink, to a minimum.
- Names, IDs, states and nodes are never cut.
- Without a terminal the table is 80 columns wide.
- The partitions table does not shrink, since everything in it is a name.
- The rules are in `CLAUDE.md`. The scheduler README holds decisions only, not what the code does.

### Decisions that carry to every scheduler

- Calls are a budget. Each command has a fixed call count, written down.
- The environment is scrubbed.
  - A call runs without the user's defaults for that command (`SQUEUE_*`, `SACCT_*`, ...).
  - It also uses a standard time format.
- Output is read at fixed widths with a check.
  - Every field has a width and ends in a delimiter the reader verifies.
  - A name may hold the delimiter. A value cut to its width is not trusted.
  - Prefer a machine format (JSON, field lists) where the scheduler has one.
- A state map per scheduler to the seven neutral states.
  - `pending`, `running`, `success`, `failed`, `cancelled`, `notfound`, `unknown`.
  - A state the map does not know is `unknown`, never a guess.
- `failed` has a `failure`.
  - `nonzero_exit`, `timeout`, `out_of_memory`, `node_failure`, `preempted`, `deadline`.
  - A cancel is `cancelled`. A scheduler that cannot tell the two reports `failed`.
- An exit code is reported only when the job's own program ended it.
  - When the scheduler ended the job, the code it carries is the scheduler's kill.
  - Two tools of one scheduler can disagree on that code.
- `notfound` is never `success`.
  - An ID with no record is `notfound`.
  - A failed history call is `unknown`, unless the site says it keeps no history.
- Estimates are not reported: a waiting job's start, a running job's end.
- Ownership comes from the listing, not from the cancel tool.
  - SLURM's `scancel` is silent for a job of another user, so its silence says nothing.
  - `cancel` lists your jobs first and sends only IDs it found.
  - An ID is `sent` unless the tool printed an error line that names it.
  - Output that names no ID it was given fails the whole call.
- An array is read as one row per task, never as a range.
  - It is shown as one row with counts.
  - A task ID is the form `jobs` takes as an argument.
- Resources are what the scheduler reports, never what condatainer asked for.
  - The request while a job waits, the allocation once it runs.
  - Where the allocation field lacks a resource, the scheduler's request field is read.
- Usage is best effort. CPU time and peak memory are shown when reported.
  - Peak memory is a lower bound.

### Facts that differ between sites

- Whether GPUs are in the allocation depends on the site's accounting.
  - One site counts `gres/gpu` and each type.
  - The other counts none, so the request is read from `tres-per-node` and `tres-per-job`.
- A submit plugin can refuse options.
  - One site refuses `-p`, an untyped GPU, and `--mem` with some GPU requests.
  - A probe script must not assume a partition.
- The controller keeps an ended job for a while, then only accounting knows it.
  - Accounting can lag: `sacct` read `PENDING` for a running job.
- A repeat cancel of an ended task prints nothing on one site and an error on the other.
  - It does not matter, since an ended job is never sent.
- Node lists come in full names or in a bracket form such as `node[01-02]`. Both are expanded.

### Still open on SLURM

- `sacct --array` on a few hundred tasks, and on the site's maximum if it is large.
  - It returns about three rows per task. Low risk.
- Load of `sacct` on a site with a busy accounting database.
- Where a `--gpus-per-task` request shows up. One site's plugin refused the test job.
- A lookup right after a job ends can read a stale state, since accounting lags.
- A cancel of another user's job through `cancel`. The design never sends it, and it is untested.
- A job `squeue` lists as ended that `sacct` does not have yet. It shows no usage, source `queue`.

### Left out for now

- `jobs --format FIELDS` and a `scheduler.jobs_format` setting.
- Other users' jobs, and a listing of finished jobs (`sacct -X -S now-7days`, one call).
- A start estimate for a pending job (`squeue --start`).
- A `PARTITION` column in the table. A pending job lists many partitions. The JSON has them.
- Heterogeneous jobs (`#+#` IDs) have no special case. Each component is a row.
- The limit on running tasks of an array (`%3`). The output of `-r` grows with the array.
- Live usage of a running job (`sstat`).
- Partitions: queue depth, idle CPUs, exact node sizes, and a GPU-type-first view.
- A check of a request against partition limits at submit time.
  - It would warn only, since limits miss QOS caps and reserved cores.

### Method that worked, to repeat

1. Read the scheduler's full documentation for each call. Write the plan with the call budget.
2. Write a probe script for the user to run on the cluster.
   - Name its jobs, make it cancel what it made, and check it with `bash -n`.
   - Never edit a script while it runs.
3. Cover every uncertain part in the probe, one case each. Print the time of each call.
4. Put the real output into unit-test fixtures.
   - Add a fake of the scheduler's tools to test arguments, environment and call count.
5. Where the user allows, run the built binary on a login node.
   - Use an isolated `CNT_ROOT` and home. Remove it afterward.
6. Keep the user docs out until the feature is promoted.
   - The generated CLI reference already lists the commands from their help.

## Shared code that still knows SLURM

Fix these before, or with, the first other scheduler.

- `cmd/scheduler_cancel.go`: `pickToCancel` cuts an ID at `_` to find its array.
  - The listing already has each job's `ArrayID`. Use that.
- `internal/scheduler/jobs.go`: `idLess` and `splitTaskSuffix` sort IDs of the form `123_4`.
  - PBS `123[4].server`, LSF `123[4]` and HTCondor `cluster.proc` need their own order.
  - Let the scheduler give a sort key, or compare through `ArrayID` and `TaskID`.
- The help of `jobs` and `cancel` uses `245068_4`-style IDs.
  - Make it neutral, or let each scheduler describe its IDs.
- `internal/scheduler/settings.go`: `DetectBin` and `TypeFromBin` do not know `condor_submit`.
- `ResourceLimits.State` and `IsUp()` use SLURM's word `up`.
  - PBS (enabled and started) and LSF (`Open:Active`) must map to it for `partitions`.
- `ResourceSpec.SelectExtra` is a PBS-only field in a shared struct.
- The existing `GetJobStatus` of PBS, LSF and HTCondor do not match the new model.
  - They return "done" when the status tool fails.
  - HTCondor calls every queued job running.
  - None reads a finished job's result. `jobs` and `cancel` must not reuse them.

## Other schedulers

None of this is verified.
State words come from the existing `GetJobStatus` of each scheduler and from documentation.
Confirm every command and field against the site's documentation and a probe before building.

### Partitions on the other schedulers

| Scheduler | Partition | Node types come from |
|---|---|---|
| PBS | queue | `pbsnodes -a`: `ncpus`, `mem`, `ngpus`, `gpu_model` per node |
| LSF | queue | `lshosts -w` per host and `lshosts -gpu` per GPU |
| HTCondor | one synthetic `default` | `condor_status` per machine: CPUs, memory, `TotalGpus` |

- PBS groups nodes by CPUs and GPUs. A node with no `queue` has no queue.
- PBS has no standard GPU type field. `gpu_model` is a site-defined name, read with `gpu_type`.
  - A site may use another name or none. Confirm it on a real cluster.
- LSF does not link hosts to queues in these calls. HTCondor reports no GPU type.
- A node type with no partition applies to every partition.
- Open on LSF: whether `lshosts -gpu` without a host lists every host.
  - Whether `ncpus` matches what a job can use, and which memory unit the site prints.
- Open on PBS and HTCondor: their output needs a run on a real cluster.
- PBS drops a disabled queue today. It should show it as down once `State` is filled.

### PBS (PBS Pro and OpenPBS, not Torque)

Commands:

- Queue: `qstat -f`, or a field format. Array sub-jobs need `-t`. Your jobs only, by user.
- Finished jobs: `qstat -x`, only when the site keeps history (`job_history_enable`).
- Cancel: `qdel <ids>`.
- Partitions: `qstat -Q`, `qstat -Qf`, `pbsnodes` (existing `GetClusterInfo`).

Facts to build on:

- IDs: `123.server`, an array parent `123[].server`, a sub-job `123[4].server`.
  - `IsJobID` must accept the server suffix, which may be absent in a short form.
  - A task is a sub-job, shown under its parent.
- States (to confirm):
  - `Q`, `H`, `W`, `T` map to `pending`. `R`, `E`, `B`, `S` map to `running`.
  - `F` and `X` are finished. The result comes from `Exit_status`.
  - A crashed job is still `F`, so success needs `Exit_status` 0.
- Failure:
  - `Exit_status` of 256 plus a signal means a signal ended the job.
  - A walltime or memory kill has a `comment` and its own values.
  - A `qdel` ends as a signal exit, so a cancel cannot be told from a crash.
  - Report `cancelled` as `failed` until a field separates them.
- Usage: `resources_used.cput`, `resources_used.mem`, `resources_used.walltime`.
  - Memory can be the sum over nodes.
- Resources: `Resource_List.select`, `ncpus`, `mem`, `ngpus`, and `exec_vnode` for nodes.
  - A select with several chunks must be summed.
- With no job history, a finished job is `notfound` or `unknown`, never `success`.

Open:

- The `qstat` field format, and how arrays and history appear.
- `Exit_status` for a walltime kill, and the site's history retention.
- What `qdel` prints for an ended or foreign job, and whether a manager can delete foreign jobs.

### LSF

Commands:

- Queue: `bjobs`, with `-o` for chosen fields and `-json` where the version has it.
- Finished jobs: `bjobs -a` for recent ones, then `bhist -l` for the record.
- Cancel: `bkill <ids>`.
- Partitions: `bqueues`, `bhosts` or `lshosts` (existing `GetClusterInfo`, untested).

Facts to build on:

- IDs: `123`, an array element `123[4]`, the array `123[1-10]`. A task is an element.
- States:
  - `PEND`, `PSUSP`, `WAIT` map to `pending`. `RUN`, `USUSP`, `SSUSP` map to `running`.
  - `DONE` is `success`, `EXIT` is `failed`, `UNKWN` and `ZOMBI` are `unknown`.
- Failure:
  - `EXIT` has an exit code.
  - A termination reason (`TERM_RUNLIMIT`, `TERM_MEMLIMIT`, `TERM_OWNER`, ...) is in `bhist -l`.
  - It would give `timeout`, `out_of_memory` and `cancelled`.
  - Whether `-o` can read it is not confirmed.
- Usage: CPU time and `MAX MEM` from `bjobs -l` or `bhist -l`.
  - The unit follows the site's `LSF_UNIT_FOR_LIMITS`.
- Resources: slots, `rusage[mem=...]` per slot, `span[hosts=1]`, `-gpu` requests.
- `bkill` prints one line per ID. Read those lines like SLURM's after confirming the wording.

Open:

- The `-o` field names of the site's version, and where the termination reason is readable.
- How array elements appear, and how long `bhist` keeps a record.
- Whether `bkill` of another user's job fails for a normal user.

### HTCondor

Commands:

- Queue: `condor_q` with JSON or attribute output (`-json`, `-af`), by owner.
- Finished jobs: `condor_history <cluster.proc>`.
  - Or `condor_history -userlog <file>` on the `.condor.log` condatainer writes.
- Cancel: `condor_rm <ids>`.
- Partitions: `condor_status` (existing `GetClusterInfo`). There are no partitions as such.

Facts to build on:

- IDs: `cluster.proc`. A cluster of many procs is the array. A bare cluster names all its procs.
- States, by `JobStatus`:
  - 1 (idle) and 5 (held) map to `pending`.
  - 2 (running), 6 (transferring output) and 7 (suspended) map to `running`.
  - 4 (completed) is `success` or `failed` by `ExitCode`.
  - 3 (removed) is `cancelled`, which HTCondor reports natively.
- Failure: `ExitCode`, `ExitBySignal`, `ExitSignal`.
  - A held job has a `HoldReason`. Memory or time limits show there.
  - A removed job may have a `RemoveReason`.
  - Decide with a real cluster whether a limit hold is `pending` or a failure.
- Usage: `RemoteUserCpu` plus `RemoteSysCpu` for CPU time, `MemoryUsage` for peak MB.
- Resources: `RequestCpus`, `RequestMemory`, `RequestGpus`.
- Dependencies: none, since they need DAGMan. `DependsOn` stays empty.
- History is local to the schedd, and a pool can have several schedds.

Open:

- JSON output on the site's version, and where history lives.
- Parsing the per-job `.condor.log` events.
- What `condor_rm` prints for a missing job, and whether it can remove another user's job.
- Pools with more than one schedd.

## Build order

1. Fix the shared code above, with tests, so SLURM behavior is unchanged.
2. For each scheduler, in the order a cluster is available:
   - Write its probe script covering every open item. The user runs it.
   - Read the full documentation of each call.
   - Write its section here with the call budget and the state map.
3. Implement `JobLister`, then `JobCanceller`, behind the same interfaces.
   - Test on fixed output from the probe, on fakes of its tools, and on the shared fake lister.
4. Update the README decisions and the help only for what that scheduler does differently.
5. Write the user docs only when the feature is promoted. See the next section.

Do not build a scheduler from documentation alone.
SLURM showed three things a reading did not:

- a site that counts no GPUs in the allocation;
- a cancel tool that is silent for another user's job;
- a history that lags a running job.

## Not for this plan

- Submitting. `run` is the submit path.
- Other users' jobs, and a listing of finished jobs.
- A record kept by `run` of each job it submitted.
