# internal/scheduler

One interface over SLURM, PBS, LSF and HTCondor: submit, parse a script's directives, read the job's resources.

- What was run on a real system:
  - SLURM is the tested target, run on a cluster.
  - HTCondor was run end to end in a docker container: submit, status, partitions and the variables a job gets.
  - PBS was run in a docker container: submit, job ids, job history, the job script and its log, and the variables a job gets.
  - LSF was never run. Its code was written from IBM's documentation.
- What belongs to one scheduler is in its own section at the end: Slurm, PBS, LSF and HTCondor.

## Directives

- A script's directives are parsed by whichever scheduler wrote them, then translated to the host.
  - A different script scheduler is a warning, not an error.
- Parsing has two stages.
  - Job control (name, output, partition) is critical, and an error stops it.
  - Resource geometry is best effort.
- **Unparsed.** When geometry cannot be resolved, `run`, builds and restore refuse the script, since there is nothing to regenerate it from.
  - Callers tell the three states apart: no directives, parsed, unparsed.
- A directive that changes the CPUs, ranks or nodes a job gets, in a way the geometry cannot hold, leaves the script unparsed.
  - Passing it through would make the exported `NCPU`, `MEM` and `NRANK` wrong.
  - Other directives pass through as written.
- `RawFlags` keeps every original directive. Only what is not absorbed goes to `RemainingFlags`.

## Resource priority

- The defaults are the `scheduler.ncpu`, `scheduler.mem` and `scheduler.time` settings.
  - Every parser starts from them, so a script that sets some resources gets the rest from the settings.
- A run takes each resource from the first of these that sets it: job's allocation > flags > script's resources > defaults.
  - The job's allocation counts only in a job with one rank. Flags are ignored there.
  - The job's allocation is the `CNT_*` exports of the job script, else the scheduler's own variables.
- A build passes its own defaults, and the script layer applies only when it has directives.
  - A script without any gets scheduler defaults filled in, and those must not replace the build's.

## Run and submit

- A script either runs where the process is, or becomes a new job. The two sides have separate rules.
- A run is in place in a container, and in a job without `--submit`. Any other script with resources is submitted, and so is any `--submit`.

Run side.

- The allocation of a job with one rank is the run's resources, and it wins over the script's headers.
  - A run in a job sets them from the job's size every time, so `NCPU` always equals `CNT_NCPU`.
  - A job that CondaTainer's job script started has the same values already.
  - Outside a job the script and the flags decide, and they set the variables.
  - A job that reports no memory gets `MEM` from the script's `#MEM` or the `scheduler.mem` setting, and a warning says it is a guess.
    - The fallback keeps `MEM` set. A tool that takes `--localmem $MEM_GB` fails without it, and a tool left to its own default may take most of the node.
    - A script whose directives did not parse has no fallback. It gets the job's memory or none.
- A job with more than one rank, an MPI job, is not read.
  - `mpiexec` and the tool place the ranks, and each rank gets the MPI library's variables.
  - The only read of ranks is their count, as a yes or no: `CNT_NRANK` first, then the scheduler's own.
  - Only an unset `OMP_NUM_THREADS` is filled, from the CPUs of one rank.
- `-c`, `-m`, `-t` and `-g` have no effect in a job, since the allocation is fixed.
  - They warn and never fail, so one wrapper command works on a laptop and in a job.
  - A flag is checked even when it has no effect, so a typo fails the same way everywhere.
- Outside a job the flags change the variables the script sees.
  - Time is not one of them. Nothing in a run enforces or exports a time, so only a submit has one.
- `OMP_NUM_THREADS` is a default, not a setting. A value already set stays, unless `-c` is typed outside a job.
  - Only a run sets it. A job script, a helper job and a build do not.
  - A value from the submitting shell reaches the job, and the run in the job fills it only when it is unset.
  - A helper script that wants the same default writes `export OMP_NUM_THREADS=${OMP_NUM_THREADS:-$NCPU}` itself, since `NCPU` is exported to it.
- A script with no resources runs with the defaults.
- `exec` and `e` run a tool as it is, and set only `NCPU`, as a convenience.
  - They set it from the job's CPUs in a job with one rank, every time. A job with more than one rank gets none.
  - A shell opened in a job that no job script started, such as `salloc` or `srun --pty`, has no header exports, so it would have no `$NCPU` to use.
  - They never set `OMP_NUM_THREADS` or the layout variables.

Submit side.

- A submitted job is a new job. The job that submits it never supplies its resources, whether it is a job, a container or a login node.
- `--submit` of a script with no resources uses the defaults, as any job does.
- A node count the script does not set is not written, and the scheduler places the ranks.
- `-m` is memory per rank.
  - With one rank it is the memory of the node, as typed.
  - With more it is per CPU, which rounds up, since the ranks per node may be unknown.
- A run's job files are named `<script>_<YYYYMMDD_HHMMSS>`, and a name that is taken gets `-2`, `-3`, ...
  - A loop of submits puts several in one second, and a shared name would run one job's script for another.
  - The name is taken by creating the job script only when its path is free, so two processes never take one name.
  - `-N`, not `_N`: an array's per-element logs are `<name>_<index>_<arg>.log`, which `_N` would make ambiguous.
  - A finer timestamp would lengthen every path, and paths have a length cap.
  - A build or restore job keeps one name per target and rewrites its file.

Both sides.

- The real run and the dry run read one list of refusals, so they never disagree.
  - `--array` needs a submitted job.
  - `--writable` is refused with `--array`, with more than one rank, and in a job with more than one rank, since a writable overlay is locked to one process.
  - Ranks need `mpiexec` on `PATH`. A scheduler can refuse them too.

## Resource headers

- `#NCPU:`, `#MEM:`, `#TIME:` and `#GPU:` ask for resources without naming a scheduler.
- One reader serves every script type, so a value means the same everywhere.
- A bare `#MEM:` number is GB and a bare `#TIME:` number is hours.
- A GPU request is `N`, `TYPE` or `TYPE:N`. A bare count has no type, and the type is lower case.
- An empty value is the same as no header.
- A value that does not parse, or a header written twice, is an error naming its line. What did parse is still returned.
- A resource header can be conditional on overlay slots: `#MEM: 56G genome=grch38`.
  - The caller binds the overlay slots first and passes the exact overlay names. The reader never resolves an overlay.
  - The first conditional line whose overlay slots all match wins, wherever it is written. The unconditional line is the fallback, so its position does not matter.
  - A header whose conditional lines all fail and that has no unconditional line is refused, since falling back to the scheduler's default would hide that the overlay has no entry.
  - Every line is checked, chosen or not, so a typo is caught whichever overlay is bound.
  - A script whose directives set resources refuses a conditional header, since ignoring it would hide the condition.
  - A reader given no overlay slots, such as a build recipe, refuses any condition.

## Which source a script is read from

- The resources come whole from the first source that sets one: the current scheduler's directives, the `#KEY:` headers, another scheduler's directives.
  - They are never merged field by field, since one script would mean different jobs depending on which lines were read last.
  - A directive sets a resource when the resource parser records it. A flag neither parser records, such as `--qos`, passes through and does not hide the headers.
  - Directives whose resources do not parse stay unparsed. The headers do not replace them.
- Job control is read from both the directives and the headers: name, output, error, partition and account.
  - A field named in both is refused, since one script would name two values.
- A header is written for no scheduler, so nothing in it is translated.
- Another scheduler's directives lose their partition and account on translation, because those names belong to one cluster.
- Any resource or job header makes the script a job, as any directive does.
- A submit description file keeps its own rules and reads no header.

## Script directives

- A scheduler reads its directives from the header block of a script and stops at the first command. A directive after it is ignored and warned about.
- Reading the same lines keeps one script meaning one thing, whoever reads it.
- A submit description file is not a script and is read whole.

## Normalized variables

- Recipes and scripts read `$NCPU`, `$MEM` and `$MEM_GB` on every scheduler, so they never learn a scheduler's own names.
  - `NCPU` is the CPUs of one rank, not threads. `MEM` is the memory of one rank, in MB.
  - `NNODE`, `NRANK` and `NRANK_PER_NODE` give the layout. Only a job script header exports them. A run, `exec` and `e` never set them. Only `CNT_NRANK` is read back, for the rank count.
- Every job script exports the variables, written by one helper for all four schedulers.
  - Each is exported under its short name and as `CNT_<name>`. `OMP_NUM_THREADS` is not one of them.
  - A job with no parsed spec exports nothing.
  - One `unset` line of every name and its `CNT_` copy comes first, so a layout variable the submitting shell had, and this job leaves out, does not survive.
- `CurrentJobResources` reads the CPUs, the memory and the GPUs. The rank count is `CNT_NRANK` first, then the scheduler's variables.
  - It reads the `CNT_` copies first, so a user's own `NCPU` cannot change what is read. Only then does it ask the active scheduler.
  - A container in a job has no scheduler binary and so no active scheduler. It reads the variables of the scheduler whose job variables are set.
  - The exports describe the job as submitted. A job that a script inside it submits by hand inherits them.
  - An `srun` or `mpiexec` step inherits them too, and a step with another shape reads the job's values.
- Each reader works without the `CNT_` exports, since a job submitted without CondaTainer has none.
  - A reader reads the variables its scheduler documents. A scheduler that documents none with a running job's memory gives none.
- `NRANK_PER_NODE` is absent when the layout is free, not defaulted.
- When ranks do not divide evenly across nodes, ranks per node rounds up. The script then enforces the layout the memory figure assumed.
- A build is one rank, so its `NCPU` is all the CPUs it was given.

## Partition and account

- Both are routing and billing strings, not resource geometry.
- They are rendered to each scheduler's own directive and never validated. A name is valid on one cluster and meaningless on another.
- They are cleared on cross-scheduler translation for the same reason.

## Cluster limits

- Idle figures are optional. A scheduler that does not report them leaves them unset, and the table omits what they fill.
  - Idle is a hint. Reservations, account limits and shared nodes can still keep a job waiting.
- A figure the scheduler gives as a minimum is reported as a lower bound, never as exact.
- The JSON lists node types exactly. The table joins types whose memory is within 10%, so a few MB do not add a row.
- The partition table never cuts a value. A GPU type is copied into a job request, so it stays whole.
- A GPU type keeps the CPUs and memory of its own nodes.
  - A partition's own figures are the largest over its node types, so they do not describe a GPU node.
- The JSON always lists every partition with its state.
- A partition with no default is normal.
  - The site's submit plugin may choose one, so a job names no partition unless the user does.

## Jobs and cancel

- The model is scheduler-neutral, so another scheduler adds no command or table code.
  - A scheduler without it is refused by name. There is no half implementation.
- Call counts are fixed, since each call loads the scheduler's server.
- The table fits the terminal by cutting NAME, NODE and NOTE. `--no-trunc` and output that is not a terminal cut nothing.
- A listing reads the times, log paths and process layout only for `--json` and `--detail`. The table does not show them.
- A call runs without the user's defaults for that tool, with a standard time format.
  - They reshape the output, and no option switches each of them off.
- Output is read at fixed widths with a delimiter check, since a job name may hold the delimiter.
- A state the map does not know is `unknown`, never a guess.
- An exit code is reported only when the job's own program ended it.
  - When the scheduler ended the job, the code is its kill, and its tools can disagree on it.
- An ID with no record is `notfound`, never `success`.
  - A failed history call is `unknown`, unless the site says it keeps no history.
- A waiting job's start and a running job's end are estimates, so they are not reported.
- Ownership comes from the listing of the user's jobs, not from the cancel tool.
  - The tool's output is not trusted to say what it cancelled.
  - Output that names no ID it was given fails the whole call.
- Peak memory is a lower bound, and CPU time depends on the site's accounting.
- Ranks per node are shown only when the user fixed them.
  - Otherwise the scheduler places the ranks, possibly unevenly, and only the totals are known.

## Excluded nodes

- Each scheduler's script writer adds `scheduler.exclude_nodes`, so run, build, restore and helper jobs all get it.
- Priority is the config list, then the script's own directive, then `--exclude-nodes`. Each replaces the one before, and an empty `--exclude-nodes` clears it.
- Config layers merge, so a site's list and a user's both apply.
- A translated script does not carry the list. Node names mean nothing on another cluster.
- Names are plain host names, as the scheduler prints them.

## The job's environment

- Every job gets the whole submit environment, on every scheduler.
  - The install, the Apptainer path and an MPI library all arrive through variables a module sets. A job without them fails far from the cause.
  - Nothing is re-exported in the script.
- A script's own limit on the environment is replaced, and the user is told. Variables it added are kept.
- A site's submit plugin that strips the environment is out of reach.
- A job's command names the binary by its full path, so the job runs the version that wrote its script.

## Dependencies

- `Submit` refuses a dependency the scheduler cannot express instead of dropping or downgrading it.
- `run` checks before it builds or submits anything, so a refusal leaves nothing half-submitted.

## Running inside a job

- `JobExecCommand` gives the prefix that runs a process on a running job's node, for the dashboard's scheduler connect mode.
- Only SLURM has one. The others return `ErrExecUnsupported`, since none has a command verified to work from outside the job.

## Settings

- The `scheduler.*` keys are declared in `settings.go`, and a key for one scheduler type in that scheduler's file (`scheduler.slurm.emit_mem` in `slurm.go`).
  - Removing a scheduler removes its keys with it.
- Whether jobs are submitted is `Enabled()`, not a key. It needs `scheduler.submit_job` on and a binary that can be run.
  - It is computed on every call, so `--no-submit` and a test override both reach it.
  - The binary is the configured one, else the first of `sbatch`, `qsub`, `bsub` and `condor_submit` on `PATH`. That search runs once.

# Slurm

## Directives

- A SLURM directive that changes the CPUs, ranks or nodes a job gets, in a way the geometry cannot hold, leaves the script unparsed.
  - A `--nodelist` longer than `--nodes` is one, since it raises the node count.

## Job variables

- `SLURM_CPUS_PER_TASK` is per rank.
- Memory is read per CPU before per node. A job with `--mem-per-cpu` can also carry a `SLURM_MEM_PER_NODE` that is not CPUs times per-CPU memory.
- A job reports memory only when the user asked for it with `--mem` or `--mem-per-cpu`.

## Cluster limits

- Partitions come from one grouped `sinfo -e` call, never `sinfo -N`.
  - The node-based listing is orders of magnitude slower on a large cluster.
  - `-e` keeps nodes of different memory in separate rows. Without it `sinfo` shows only the smallest, as `748G+`.
- Idle nodes and CPUs come from `%F` and `%C` of the partition call, so they add no call.
  - A node with any job counts as allocated, so idle nodes understate the room. Idle CPUs are shown too.
  - Idle CPUs are those of nodes that take jobs. The total includes down and drained nodes.
- Idle GPUs come from a second grouped `sinfo -e -O` call, since `GresUsed` exists only in `-O`.
  - It covers only the partitions that have GPUs, and never uses `-N`.
  - Each row carries the partition, CPUs, memory and GPUs, so it finds its node type without a node list.
  - Only idle, mixed and allocated nodes count. State flags such as `-` and `*` are stripped first.
  - Each partition counts its own nodes. One node is in many partitions, so figures are never added across them.
  - A cut value or an unreadable line leaves the figure unreported, since a wrong figure is worse.
  - A failed call leaves the figures unreported and does not fail the listing.
- A call to `sinfo` runs without the user's defaults for that tool.

## Jobs and cancel

- `scontrol` is not called. `squeue -t all` gives the same records.
- `JobID` is the raw ID of each array element, so a task comes from `ArrayJobID` and `ArrayTaskID`.
- Whether `tres-alloc` counts GPUs depends on the site.
  - Where it does not, the request fields are read.
- `sacct -X` zeroes `TotalCPU`, so `sacct` runs without it.
- `squeue` and `sacct` give different exit codes for a job the scheduler ended.
- `scancel --me` is silent and exits 0 for a job of another user.
- One malformed ID makes `scancel` cancel none of the call, so only found IDs are passed.

## Excluded nodes

- A script's directive `--exclude` is read.
- A short name is refused when the node is registered by its full name.

## Running inside a job

- `srun --jobid J --overlap` runs a process on the job's node.

# PBS

- PBS means PBS Pro or OpenPBS. Torque is not supported.

## Job variables

- PBS has no per-rank variable. `NCPUS` and `OMP_NUM_THREADS` are both the first chunk's `ompthreads`, else its `ncpus`, which counts every rank on the node.
  - `NCPUS` is not the exported name, since one name would mean two things.
  - The reader takes `NCPUS`. It is right for one rank per chunk, such as an interactive allocation, and for a chunk that sets `ompthreads`.
  - A run finds `OMP_NUM_THREADS` already set, so it adds nothing.
- PBS Pro sets `PBS_TASKNUM=1` in every job, so it is not read. The ranks and nodes are the lines and the distinct hosts of `$PBS_NODEFILE`.
- A shell opened with `ssh` on a node of an MPI job is not supported.
- PBS documents no variable with a running job's memory, so a job gives none.

## Select, time and log

- Ranks with no node count are one chunk per rank, with no `place`, so the site's default decides. PBS Pro's own default is `free`. The memory of each chunk is the memory of one rank.
- Ranks that do not divide over the nodes are two chunks.
  - Full nodes take the rounded-up count and one node takes the rest, when that fills the nodes exactly.
  - Otherwise the remainder nodes take one rank more than the others, as SLURM's block layout does.
  - A node left with no rank is dropped.
- A select count is chunks, and a chunk is taken from one vnode. The writer sets no `place=scatter`, so `NNODE` is the chunk count, which a host shared by two chunks makes too high.
- One log for both streams is `-o` with `-j oe`, never `-o` and `-e` on one path, since PBS copies one stream over the other.
- PBS Pro has no working directory directive, so the job script changes into it. `#PBS -d` is TORQUE's, and `qsub` rejects it.
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

## Excluded nodes

- PBS has no directive for it, so a PBS submit with any list set is refused.

# LSF

## Job variables

- LSF reads no per-rank variable. Each LSF slot counts as one rank with one CPU, which is wrong with `affinity[cores(C)]`, where a slot is C cores.
  - The node count is not read, though `LSB_HOSTS` lists the hosts.
  - `LSB_DJOB_NUMPROC` and `LSB_MAX_NUM_PROCESSORS` were not confirmed against IBM's documentation.
- LSF documents no variable with a running job's memory, so a job gives none.

## Time

- LSF `-W` takes minutes and `HH:MM`, so LSF and PBS have separate time parsers.

## Memory

- `rusage[mem=X]` is a per-slot scheduling reservation, and it is the memory source when parsing.
- `-M` is a limit, not a request. It is never read as memory and passes through as-is.
- On generation `-M` is emitted only when the site enforces memory, detected from the LSF environment.
  - The limit is 10% over the request.
  - Whether `-M` is per process or per job depends on the site's mode, so the base differs.

## Cluster limits

- Hosts and GPUs come from `lshosts`, not `bhosts`. This is untested on a real cluster.

## Excluded nodes

- A script's directive `-m` is read. An LSF `-m` cannot be merged with a list, so it stands over the config list.

## Running inside a job

- `blaunch` works only inside an LSF job.

# HTCondor

## Submit

- A submit of more than one rank is refused.
  - A vanilla job is one HTCondor slot on one machine, and MPI across machines needs the parallel universe.
  - MPI inside one container needs nothing from CondaTainer: the script asks for one large job and runs its own `mpiexec`.
- A job asks for the memory of its one rank, whether the spec gave it per node or per CPU.
- The job script is read when the job starts, so a shared job file name would run another job's script.
- A job has no account. Its `accounting_group` is a fairshare group, so it fills the partition and the account stays unset.
- A dependency is not supported, since that would need DAGMan.

## Job variables

- HTCondor sets no variable with the request. The reader takes `CpusProvisioned`, `MemoryProvisioned` and `GpusProvisioned` from the job ad, what the slot gave.
- `CUDA_VISIBLE_DEVICES=10000` is set in a job without GPUs, to hide them. The GPU count is never read from that variable.
