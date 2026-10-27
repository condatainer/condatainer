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
- **Unparsed.** When geometry cannot be resolved, `run`, builds and restore refuse the script, since there is nothing to regenerate it from.
  - Callers tell the three states apart: no directives, parsed, unparsed.
- Some SLURM directives have no place in the geometry and leave the script unparsed, for example `--gpus-per-socket` and `--distribution`.
- `RawFlags` keeps every original directive. Only what is not absorbed goes to `RemainingFlags`.

## Resource priority

- Defaults, then the script's directives, then the job's own non-zero fields.
- The script layer applies only when it has directives. A script without any gets scheduler defaults filled in, and those must not replace the caller's.

## Resource headers

- `#NCPU:`, `#MEM:`, `#TIME:` and `#GPU:` ask for resources without naming a scheduler.
- One reader serves every script type, so a value means the same everywhere.
- A bare `#MEM:` number is GB and a bare `#TIME:` number is hours.
- A GPU request is `N`, `TYPE` or `TYPE:N`. A bare count has no type, and the type is lower case.
- An empty value is the same as no header.
- A value that does not parse, or a header written twice, is an error naming its line. What did parse is still returned.

## Which source a script is read from

- A script may carry the current scheduler's directives, `#KEY:` resource headers and another scheduler's directives.
- The first it has is read whole, in that order. Nothing is merged field by field.
  - Merging would let one script mean different jobs depending on which lines were read last.
  - A skipped source prints one note naming its first line.
- A header is written for no scheduler, so its result carries the host's type. Nothing is translated, and a partition or account in a header is used as written.
- Directives of another scheduler lose their partition and account on translation, because those names belong to one cluster.
- A resource, partition or account header makes the script a job. A name or a log path alone does not.
- A submit description file keeps its own rules and reads no header.

## Script directives

- A scheduler reads its directives from the header block of a script and stops at the first command. A directive after it is ignored and warned about.
- Reading the same lines keeps one script meaning one thing, whoever reads it.
- A submit description file is not a script and is read whole.

## Normalized variables

- Recipes and scripts read `$NCPU`, `$MEM` and `$MEM_GB` on every scheduler, so they never learn a scheduler's own names.
  - `NCPU` is the CPUs of one rank, not threads. `MEM` is the memory of one rank, in MB.
  - `NNODE`, `NRANK` and `NRANK_PER_NODE` give the layout.
  - `NCPUS` is not used: PBS sets it to the CPUs of the first chunk, so one name would mean two things.
- Every job script exports the variables, written by one helper for all four schedulers.
  - Each is exported under its short name and as `CNT_<name>`. `OMP_NUM_THREADS` has no copy.
  - A job with no parsed spec exports nothing.
- `CurrentJobResources` reads the `CNT_` copies first, so a user's own `NCPU` cannot change what is read. Only then does it ask the active scheduler.
  - The exports describe the job as submitted. A job that a script inside it submits by hand inherits them.
  - An `srun` or `mpiexec` step inherits them too, and a step with another shape reads the job's values.
  - A rank on another node gets its environment from `mpiexec`. It may have none of them and falls back to the scheduler's own variables.
- Each scheduler's reader stays correct on its own, since a job submitted without CondaTainer exports nothing.
  - SLURM reads per-task variables.
  - LSF has no node count or tasks per node, so its reader gives the slot count only.
  - PBS has no per-rank variable. `NCPUS` is the first chunk's `ncpus`, which counts every rank on the node, and `OMP_NUM_THREADS` is that chunk's `ompthreads`, else its `ncpus`.
    - The reader takes `NCPUS`. It is right for one rank per chunk, such as an interactive allocation.
    - A shell opened with `ssh` on a node of an MPI job is not supported, and nothing is derived from `$PBS_NODEFILE`.
- `NRANK_PER_NODE` is absent when the layout is free, not defaulted.
- When tasks do not divide evenly across nodes, tasks per node rounds up. The script then enforces the layout the memory figure assumed.
- A build is one rank, so its `NCPU` is all the CPUs it was given.

## PBS select and time

- A select with several `+` chunks is accepted only when it is consistent. Otherwise it is rejected: no spec is built, and `run` refuses to submit the script.
  - CPUs per rank are the same in every chunk, and `ncpus` divides by `mpiprocs`.
  - Memory is the same per CPU in every chunk, or the same per node in every chunk, or absent from all of them. Memory in some chunks only is rejected.
- A select resource outside `ncpus`, `mpiprocs`, `ompthreads`, `mem` and `ngpus` is kept as written, in the same order, and written into every chunk.
  - Site resources such as `gpu_id` or a GPU model mean nothing to another scheduler, so only PBS writes them.
  - Chunks that differ in them, or a part that is not `key=value`, leave the script unparsed.
- `ompthreads` is the CPUs per rank, written in every chunk with more than one rank.
  - A read value that differs has no field in the spec, so the script is unparsed.
  - A script that wants other thread counts sets `OMP_NUM_THREADS` in its body.
- A PBS time is `[[HH:]MM:]SS`, so a bare number is seconds and `1:30` is a minute and a half.
- LSF `-W` takes minutes and `HH:MM`, so the two have separate parsers.

## Partition and account

- Both are routing and billing strings, not resource geometry.
- They are rendered to each scheduler's own directive and never validated. A name is valid on one cluster and meaningless on another.
- They are cleared on cross-scheduler translation for the same reason.
- HTCondor has no account. Its `accounting_group` is a fairshare group, so it fills the partition and the account stays unset.

## Excluded nodes

- Each scheduler's script writer adds `scheduler.exclude_nodes`, so run, build, restore and helper jobs all get it.
- Priority is the config list, then the script's own directive, then `--exclude-nodes`. Each replaces the one before, and an empty `--exclude-nodes` clears it.
  - A script's directive is read for SLURM `--exclude` and LSF `-m`. An LSF `-m` cannot be merged with a list, so it stands over the config list.
- Config layers merge, so a site's list and a user's both apply.
- A translated script does not carry the list. Node names mean nothing on another cluster.
- Names are plain host names, as the scheduler prints them. SLURM refuses a short name when the node is registered by its full name.
- PBS has no directive for it, so a PBS submit with any list set is refused.

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
