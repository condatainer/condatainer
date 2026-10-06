# internal/scheduler

One interface over SLURM, PBS, LSF and HTCondor: submit, parse a script's directives, read the job's resources.

- SLURM is the tested target. PBS, LSF and HTCondor are experimental.
- PBS means PBS Pro or OpenPBS. Torque is not supported.

## Directives

- A script's directives are parsed by whichever scheduler wrote them, then translated to the host.
  - A PBS host can run a SLURM script.
  - A different script scheduler is a warning, not an error.
- Parsing has two stages.
  - Job control (name, output, partition) is critical, and an error stops it.
  - Resource geometry is best effort.
- **Passthrough.** When geometry cannot be resolved, the directives are forwarded untouched.
  - Callers tell the three states apart: no directives, parsed, passthrough.
- Some SLURM directives have no place in the geometry and force passthrough, for example `--gpus-per-socket` and `--distribution`.
- `RawFlags` keeps every original directive. Only what is not absorbed goes to `RemainingFlags`.

## Resource priority

- Defaults, then the script's directives, then the job's own non-zero fields.
- The script layer applies only when it has directives. A script without any gets scheduler defaults filled in, and those must not replace the caller's.

## Normalized variables

- Recipes and scripts read `$NCPUS`, `$MEM` and `$MEM_GB` on every scheduler, so they never learn a scheduler's own names.
- LSF and HTCondor lack enough native variables, so the values are exported in the generated batch script.
- SLURM and PBS have them, so they are computed when entering a container.
- `NTASKS_PER_NODE` is absent when the layout is free, not defaulted.
- When tasks do not divide evenly across nodes, tasks per node rounds up. The script then enforces the layout the memory figure assumed.
- `MEM` is memory per task.

## Partition and account

- Both are routing and billing strings, not resource geometry.
- They are rendered to each scheduler's own directive and never validated. A name is valid on one cluster and meaningless on another.
- They are cleared on cross-scheduler translation for the same reason.
- HTCondor has no account. Its `accounting_group` is a fairshare group, so it fills the partition and the account stays unset.

## LSF memory

- `rusage[mem=X]` is a per-slot scheduling reservation, and it is the memory source when parsing.
- `-M` is a limit, not a request. It is never read as memory and passes through as-is.
- On generation `-M` is emitted only when the site enforces memory, detected from the LSF environment.
  - The limit is 10% over the request.
  - Whether `-M` is per process or per job depends on the site's mode, so the base differs.

## The job's environment

- Every job gets the whole submit environment, on every scheduler.
  - The install, the Apptainer path and an MPI library all arrive through variables a module sets. A job without them fails far from the cause.
  - Nothing is re-exported in the script.
- A script's own limit on the environment is replaced, and the user is told. Variables it added are kept.
- A site's submit plugin that strips the environment is out of reach.
- A job's command names the binary by its full path, so the job runs the version that wrote its script.

## Dependencies

- `Submit` refuses a dependency the scheduler cannot express instead of dropping or downgrading it.
- HTCondor supports none, since that would need DAGMan.
- `run` checks before it builds or submits anything, so a refusal leaves nothing half-submitted.

## Running inside a job

- `JobExecCommand` gives the prefix that runs a process on a running job's node, for the dashboard's scheduler connect mode.
- Only SLURM has one, `srun --jobid J --overlap`.
- PBS, LSF and HTCondor return `ErrExecUnsupported`. None has a command verified to work from outside the job. `blaunch` works only inside an LSF job.

## Settings

- The `scheduler.*` keys are declared in `settings.go`, and a key for one scheduler type in that scheduler's file (`scheduler.slurm.emit_mem` in `slurm.go`).
  - Removing a scheduler removes its keys with it.
- Whether jobs are submitted is `Enabled()`, not a key. It needs `scheduler.submit_job` on and a binary that can be run.
  - It is computed on every call, so `--no-submit` and a test override both reach it.
  - The binary is the configured one, else the first of `sbatch`, `qsub` and `bsub` on `PATH`. That search runs once.
