# Memo: what a scheduler tells a job about its size

A job learns its CPUs, memory, ranks and GPUs from environment variables. What each scheduler sets differs, and much of it is set only when the user asked for it. CondaTainer's job scripts export `CNT_*` copies for that reason.

Each fact is marked: **doc** is the scheduler's documentation, **measured** is a real job. SLURM was measured on one cluster, HTCondor in the `htcondor/mini` container, PBS Pro 19.0.0 in the `pbspro/pbspro` container. LSF is from its documentation only.

## SLURM

- `SLURM_JOB_ID`: always (doc).
- `SLURM_NTASKS`: only with `--ntasks` or a `--ntasks-per-*` option (doc).
- `SLURM_CPUS_PER_TASK`: only with `-c` or `--tres-per-task=cpu=` (doc). The measured cluster set it in every job, which is a site setting.
- `SLURM_CPUS_ON_NODE`: always (doc). It counts hyperthreads: a `-n 1` job showed 2 (measured).
- `SLURM_MEM_PER_NODE` matches `--mem` and `SLURM_MEM_PER_CPU` matches `--mem-per-cpu`, in MiB (doc).
- `-n 4 -c 2 --mem-per-cpu=1G` set both: `SLURM_MEM_PER_CPU=1024` and `SLURM_MEM_PER_NODE=16384`, twice the request (measured). Per-CPU is the one to read.
- `SLURM_TASKS_PER_NODE` is written like `2(x2)` (doc, measured).
- `SLURM_GPUS_ON_NODE`: GPUs of the step on this node (doc).
- An `srun` step sets `SLURM_CPUS_PER_TASK` only with `-c` for that step (doc).

## PBS Pro and OpenPBS

Running PBS Pro in Docker, to test a job here: the `pbspro/pbspro` image (PBS Pro 19.0.0, CentOS 7).

- Start it with `-h pbs` and `-it`. The entrypoint starts PBS and opens a shell as `pbsuser`.
  ```bash
  docker run -it --name pbs -h pbs -v "${PWD}:/test" pbspro/pbspro
  ```
- Jobs stay queued until two things are fixed:
  - `/etc/pbs.conf` has `PBS_START_MOM=0`, so `pbs_mom`, the daemon that runs jobs on a node, never starts. `pbsnodes -a` then says `Server has no node list`, or `state-unknown,down` once a node is created.
  - The server has no node.
- Fix both as root, once per container. `pbs` in the last two lines is the host name from `-h pbs`.
  ```bash
  sed -i 's/^PBS_START_MOM=.*/PBS_START_MOM=1/' /etc/pbs.conf && /etc/init.d/pbs restart
  /opt/pbs/bin/qmgr -c "create node pbs"
  /opt/pbs/bin/pbsnodes -a | grep 'state ='   # state = free
  ```

Facts:

- The `qsub` man page lists `PBS_JOBID`, `PBS_JOBNAME`, `PBS_ENVIRONMENT`, `PBS_NODEFILE`, `PBS_QUEUE`, `PBS_JOBDIR`, `TMPDIR` and the `PBS_O_*` variables (doc).
- It lists no variable for CPUs, memory or ranks (doc).
- `PBS_NP`, `PBS_NUM_NODES`, `PBS_NUM_PPN` and `PBS_TASKNUM` are in TORQUE's list (doc). A PBS Pro 19.0.0 job sets `PBS_TASKNUM` too (measured).
- `PBS_VMEM` is in neither PBS Pro's nor TORQUE's list.
- `PBS_NODEFILE` lists the vnodes of the job, the documented way to count ranks.
- `pbs_resources(7B)` (doc):
  - A chunk is "the smallest set of resources that will be allocated to a job", and all of it comes from one vnode, not one host.
  - The number before a chunk in `select` is the chunk count, 1 when left out.
  - Without a `place`, the arrangement is `free`. A site can set its own default. The values of `place` are in the `qsub` man page, which is not read here.
  - `mpiprocs` defaults to 1 when `ncpus` is above 0. `$PBS_NODEFILE` has one line per `mpiprocs`, summed over the chunks.
  - `ompthreads` defaults to `ncpus`. Rank 0 gets `NCPUS` and `OMP_NUM_THREADS` set to it.
- The job id carries the server: `123.server`, and `123[1].server` for a subjob.
- Measured in PBS Pro 19.0.0 jobs:
  - No variable holds the memory, whatever `mem` the job asks for.
  - `qstat -f $PBS_JOBID` shows the request, `Resource_List.mem = 1gb`, as a command's output rather than a variable.
  - `NCPUS` and `OMP_NUM_THREADS` are equal: the first chunk's `ompthreads`, else its `ncpus`.

    | Request | `NCPUS` | `$PBS_NODEFILE` lines |
    |---|---|---|
    | `select=1:ncpus=2:mem=1gb` | 2 | 1 |
    | `select=1:ncpus=4:mpiprocs=2` | 4 | 2 |
    | `select=1:ncpus=4:mpiprocs=2:ompthreads=2` | 2 | 2 |
    | `select=2:ncpus=1:mpiprocs=1` | 1 | 2 |
    | `-l ncpus=2 -l mem=1gb` | 2 | 1 |
    | none | 1 | 1 |

  - `PBS_TASKNUM=1` in every job, whatever its ranks. It is not a rank count. `PBS_NODENUM=0`, `PBS_MOMPORT` and `PBS_JOBCOOKIE` are set too, though the `qsub` man page lists none of them.
  - `PBS_NP`, `PBS_NUM_NODES`, `PBS_NUM_PPN` and `PBS_VMEM` are never set.
  - `$PBS_NODEFILE` has one line per rank (`mpiprocs`), so its lines count the ranks (doc, measured). The doc is `pbs_resources(7B)`: the lines are the sum of `mpiprocs` over the chunks.
  - A subjob's id is `7[2].pbs`, with `PBS_ARRAY_INDEX=2` and `PBS_ARRAY_ID=7[].pbs`, the array's own id.
  - Without `-V` a job gets none of the submitting shell's variables. With `-V` it gets all of them.
  - A job can submit a job here, where the node is also the server.
- Measured when submitting to PBS Pro 19.0.0:
  - `qsub` rejects `#PBS -d <dir>`: `invalid option -- 'd'`. It is TORQUE's, and PBS Pro has no directive for the working directory.
  - A job whose `-o` and `-e` name one file keeps only the stderr in it. `-o` with `-j oe` keeps both.
  - `select=4:ncpus=1:mem=2048mb` with no `mpiprocs` and no `place`, and an uneven `select=1:ncpus=3:mpiprocs=3+1:ncpus=2:mpiprocs=2`, are accepted. `qstat -f` shows the same `Resource_List.select`.
  - `qstat -x` fails with `PBS is not configured to maintain job history` until `qmgr -c "set server job_history_enable=True"` is run.
  - `qsub` prints the job id and nothing else, such as `3.pbs`, and `6[].pbs` for an array.
  - `/opt/pbs/bin/mpiexec` is PBS's wrapper. It fails with `no non-PBS mpiexec in PBS_O_PATH` when no other MPI is installed.
  - The image's glibc is 2.17, and the Apptainer CondaTainer downloads needs 2.28, so no container starts in it.

## LSF

- `LSB_JOBID`, `LSB_JOBINDEX` (0 outside an array) (doc).
- `LSB_DJOB_NUMPROC`: the slots of the job. It is not on the job-execution page, so it is unconfirmed. One rank of 4 CPUs and 4 ranks of 1 CPU look the same.
- `LSB_HOSTS` lists the hosts the job runs on, several for a parallel job (doc).
- `LSB_BIND_CPU_LIST` and `LSB_AFFINITY_HOSTFILE`: only with affinity (doc).
- No variable gives a running job its memory limit or reservation (doc).
- `LSB_MAX_MEM_RUSAGE` is set only after the job, for post-exec (doc).

## HTCondor

- No variable gives the request. `_CONDOR_REQUEST_CPUS` and `_CONDOR_REQUEST_MEMORY` do not exist (doc, measured).
- `$_CONDOR_JOB_AD` names a file with the job ad. `RequestCpus` and `RequestMemory` (MiB) are there (doc, measured). With no `request_memory`, the pool's default is filled in: 128 in `htcondor/mini`.
- `$_CONDOR_MACHINE_AD` names the slot's ad. Its `Cpus` and `Memory` are the slot's, which may be larger than the request (doc). They were equal in a dynamic slot (measured).
- `OMP_NUM_THREADS`, `MKL_NUM_THREADS` and similar are the slot's CPUs (doc, measured).
- `BATCH_SYSTEM=HTCondor` in every job (doc, measured).
- A job with no GPU has `CUDA_VISIBLE_DEVICES=10000`, which hides the GPUs (measured). Counting its entries gives one GPU.
- The GPUs are in the ads: the slot's machine ad has `GPUs`, and the job ad has `GpusProvisioned`. Both are 0 for a job with no GPU, and the job ad has no `RequestGPUs` (measured).
- A job with `request_gpus = 1` has `RequestGPUs = 1` and `GpusProvisioned = 1`, and its slot has `GPUs = 1` and `AssignedGPUs` naming the GPU. `CUDA_VISIBLE_DEVICES` and `NVIDIA_VISIBLE_DEVICES` hold the GPU's full UUID, and `_CONDOR_AssignedGPUs` and `GPU_DEVICE_ORDINAL` its short id (measured, one NVIDIA GPU through Docker).
- The job id is the cluster and the proc: `ClusterId` and `ProcId`, from 0.

## Inheritance

- SLURM `sbatch` passes the whole submitting environment by default (`--export=ALL`) (doc, measured). An `srun` step keeps the job's.
- LSF `bsub` passes everything by default (`-env "all"`) (doc).
- PBS `qsub` passes only the `PBS_O_*` variables, unless `-V` (doc).
- HTCondor passes nothing, unless `getenv = True` (doc, measured).
- A job started by hand inside a job therefore inherits that job's `NCPU`, `MEM` and `CNT_*` on SLURM and LSF. The scheduler's own variables always describe the new job (measured on SLURM).

## Submitting from inside a job

- SLURM: works on the measured cluster.
- LSF: only where the compute node is an LSF client or server host. `blaunch` is LSF's tool for work inside an allocation (doc).
- PBS (TORQUE): only the server host submits by default. A compute node needs to be a trusted submit host (doc).
- HTCondor: an execute node usually runs no schedd. In `htcondor/mini` it worked, since one container has every role.
- Inside a CondaTainer container `sbatch` is not on `PATH`. `host-run` runs with the host environment of the job, not the container's (measured).

## The cgroup limit

- The limit a job is killed at is its cgroup's `memory.max`, the smallest one up the tree from `/proc/self/cgroup`.
- It is opt-in on all four: SLURM `ConstrainRAMSpace`, the PBS `pbs_cgroups` hook, LSF `LSB_RESOURCE_ENFORCE`, HTCondor `CGROUP_MEMORY_LIMIT_POLICY` (doc). Where it is off, `memory.max` is `max`.
- The measured SLURM cluster enforced it, readable from inside a container. `htcondor/mini` showed `max`.
- A scheduler can also kill a job without cgroups, by polling its usage.

## What follows for CondaTainer

- Only the `CNT_*` exports of a CondaTainer job script carry the full size on every scheduler.
- The scheduler's own variables are a fallback for a job CondaTainer did not start, and on PBS Pro and LSF they give almost no size.
- A reader for a variable that no documentation lists reads nothing. `PBS_VMEM`, `LSB_MAX_MEM_RUSAGE` and `_CONDOR_REQUEST_*` were such readers.

## Sources

- [SLURM sbatch](https://slurm.schedmd.com/sbatch.html), [SLURM srun](https://slurm.schedmd.com/srun.html), [SLURM cgroup.conf](https://slurm.schedmd.com/cgroup.conf.html)
- [OpenPBS qsub.1B](https://raw.githubusercontent.com/openpbs/openpbs/master/doc/man1/qsub.1B), [OpenPBS pbs_resources.7B](https://raw.githubusercontent.com/openpbs/openpbs/master/doc/man1/pbs_resources.7B), [TORQUE exported variables](https://docs.adaptivecomputing.com/torque/4-2-9/Content/topics/2-jobs/exportedBatchEnvVar.htm), [TORQUE job submission](https://docs.adaptivecomputing.com/torque/2-5-12/Content/topics/2-jobs/jobSubmission.htm), [OpenPBS cgroups design](https://openpbs.atlassian.net/wiki/x/CgCx)
- [LSF job execution variables](https://www.ibm.com/docs/en/spectrum-lsf/10.1.0?topic=variables-environment-set-job-execution), [LSF variable reference](https://www.ibm.com/docs/en/SSWRJV_10.1.0/lsf_config_ref/lsf_envars_ref.html), [LSF bsub -env](https://www.ibm.com/docs/en/SSWRJV_10.1.0/lsf_command_ref/bsub.env.1.html), [LSF LSB_RESOURCE_ENFORCE](https://www.ibm.com/docs/SSWRJV_10.1.0/lsf_config_ref/lsf.conf.lsb_resource_enforce.5.html)
- [HTCondor job environment](https://htcondor.readthedocs.io/en/latest/users-manual/env-of-job.html), [job ad attributes](https://htcondor.readthedocs.io/en/latest/classad-attributes/job-classad-attributes.html), [machine ad attributes](https://htcondor.readthedocs.io/en/latest/classad-attributes/machine-classad-attributes.html)
