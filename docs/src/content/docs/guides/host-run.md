---
title: Host Run
description: Run a command on the host from inside a container, such as scheduler's commands from a helper, with full user and group identity.
---

`condatainer host-run` runs one command on the **host**, the node a container runs on, from a program inside that **container**. The output and the exit code come back.

Use it when a program inside a container needs something only the host has: scheduler's commands, such as `sbatch` and `squeue` on SLURM, or full user and group identity. A [helper](helper.mdx) such as code-server is the usual case, so a terminal in it can submit and check jobs.

```bash
condatainer host-run squeue --me      # SLURM: your jobs
condatainer host-run sbatch job.sh    # SLURM: submit a job
```

## What It Does

Inside a container scheduler's commands are not there, and your user shows only some of its groups. `host-run` runs the command on the host instead, as you, with all of them:

```text
> id
uid=1000(alice) gid=1000 groups=1000,65534(nogroup)
> condatainer host-run id
uid=1000(alice) gid=1000 groups=1000,2119(alice),4020(lab)
```

- The command starts in the directory you are in. That directory must exist on the host.
- Its standard output and standard error come back on the same streams, so on SLURM `JOB=$(condatainer host-run sbatch --parsable job.sh)` captures the job ID alone.
- `host-run` exits with the command's exit code. A command that is not found exits `127`, and one that cannot be run exits `126`.
- The command is run directly, not through a shell. For a pipe or a redirect, run a shell: `condatainer host-run bash -c 'ls | head -5'`.
- It works only in a container that was started with `host-run` turned on, as described in [Where It Is Available](#where-it-is-available). Anywhere else, including on the host itself, it stops with `this container has no host relay`.

## Where It Is Available

A container has `host-run` when the way it was started is listed in the `host_run.launch` [setting](config.md). The default is `helper`.

| Launch | The container of |
|---|---|
| `helper` | `condatainer helper run` |
| `exec` | `condatainer exec` and `condatainer e` |
| `run` | the script of `condatainer run` |

```bash
condatainer config set host_run.launch helper exec run   # add exec and run
condatainer config set host_run.launch none              # no container has it
```

- The setting is read when the container starts. A container that is running keeps what it has.
- Every config layer's list is merged, so a site's list and yours add together. `none` in any layer turns it off for every container.
- For one shell, an environment variable replaces the list: `CNT_CONFIG_HOST_RUN_LAUNCH='exec|run'`.
- A container started from inside another container has no `host-run`.

## Choose Where a Command Runs

`--on` picks the node. `host_run.on` sets the default for every call, and `--on` beats it.

| Value | The command runs on |
|---|---|
| `localhost` (default) | the node the container runs on. In a job, that is the compute node. |
| `login` | the login node the job was submitted from |
| a node name | that node, by host name or an alias from `~/.ssh/config` |

```bash
condatainer host-run --on login sbatch job.sh     # SLURM: submit from the login node
condatainer config set host_run.on login          # make that the default
```

Use `login` where compute nodes may not submit jobs.

- `login` works in a job that CondaTainer submitted, such as one from `run`, a helper, `create` or `project restore`. A job submitted from such a job still has the first login node.
- On a login node, `login` is that node.
- A command on another node, runs through `ssh` with the keys and settings on the host. It needs a passwordless login.
- The first connection to a node adds its host key to `~/.ssh/known_hosts`. A changed key is refused.

## Submit Jobs From a Job

A job that plans a pipeline can submit the steps after it. Inside a job, `condatainer run` runs its script in that job. `--submit` makes it submit a new job instead. From inside a container, it goes through `host-run` for you:

```bash
JOB=$(condatainer run --submit step1.sh)
condatainer run --submit --afterok "$JOB" step2.sh
```

- The container needs `host-run`. For jobs started by `condatainer run`, add `run` to `host_run.launch`.
- `--submit-on` picks the node the job is submitted from, as `--on` does: `localhost`, `login` or a node name. The default is `host_run.on`.
- `--submit` needs a scheduler. A script with no resources is submitted with the default job settings, the `scheduler.ncpu`, `scheduler.mem` and `scheduler.time` [settings](config.md).

## Send Input

A command reads nothing unless you send it standard input with `--stdin`:

```bash
condatainer host-run --stdin sbatch < job.sh    # SLURM: read the job script from standard input
```

- `--stdin` sends up to 1 MiB. For more, write the data to a file on the shared file system and run the command on the file.

## Safety and Limits

- Any program in the container can run any command as you on the host through `host-run`. Choose the launches that have it with `host_run.launch`.
- Closing `host-run`, for example with Ctrl-C, stops the command on the host. Over `ssh`, the command on the other node may keep running.

## If It Does Not Work

| Message | What to do |
|---|---|
| `this container has no host relay` | Start the container from a launch listed in `host_run.launch`. A container started inside another container never has one. |
| `the working directory ... does not exist on the host` | Run from a directory the host also has, such as one on the shared file system. |
| `Host key verification failed.` | The node's key changed, or this `ssh` cannot accept a new key. Check it with `ssh <node>` on the host. |
| `<command>: command not found` | The command is not on the host's `PATH`. |
