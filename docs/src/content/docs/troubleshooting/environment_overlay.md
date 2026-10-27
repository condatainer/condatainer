---
title: Environment Overlay Issues
description: Fix a writable overlay that is in use, write-protected, read-only, full, owned by another user, not shut down cleanly, or refused for writing.
---

A writable overlay (`env.img`) is one file that holds a small filesystem. When a container opens it for writing, a program called `fuse2fs` lets an ordinary user read and write that file without administrator rights.

## Not shut down cleanly

```text
env.img was not shut down cleanly. Run `condatainer overlay fscheck env.img` to check it.
```

A container stopped before it released the overlay, for example when a job reached its time limit and was killed. The overlay still opens, and nothing is changed.

Check and repair it when no container is using it:

```bash
condatainer overlay fscheck env.img
```

Add `-f` to check an overlay that is marked clean.

## In use by another process

```text
env.img is currently in use
env.img is open for writing by another process
```

A writable overlay can be open in only one container at a time, and it cannot be opened for writing while read-only containers use it. Wait for the other container to exit, or stop it. See [Writable vs. Read-Only Modes](../guides/environments.md#writable-vs-read-only-modes).

### Find what holds it

On this machine, list the processes that have the file open:

```bash
lsof env.img
```

`lsof` shows only this machine. On a cluster, a job on another node can hold the overlay. List your jobs with the scheduler's own command and cancel the one that uses it:

```bash
squeue -u $USER   # Slurm
scancel <job_id>
```

A job you started with `condatainer helper run` is stopped with [`condatainer helper stop`](../guides/helper.mdx#command-line-interface).

After a job is killed while it wrote to the overlay, [check it](#not-shut-down-cleanly) before the next use.

## Write-protected

```text
env.img is write-protected; chmod u+w to allow changes
env.img is not writable by you (owner: alice)
```

- CondaTainer never changes an overlay that you cannot write.
- Write access removed from your own file protects it on purpose. Restore it with `chmod u+w env.img` to change the overlay.
- Another user's file is theirs to change. Copy it, chown and use your copy.

## Read-only file system

```text
Read-only file system
```

Inside the container, a write failed because the overlay is mounted read-only. `exec` and `run` mount an `.img` read-only unless you add `-w`. Leave the container and open it for writing:

```bash
condatainer e env.img  # Writable by default
condatainer exec -w -o env.img bash
```

## No space left on device

```text
No space left on device
```

The overlay is full. Leave the container, then check the disk and inode usage and grow the overlay:

```bash
condatainer info env.img
condatainer overlay resize env.img -s 30G
```

To free space first, run `mm clean -a` inside the container. It removes Conda's cached index, tarballs and unused packages.

## Permission denied inside the container

Files in the overlay that belong to another user ID cannot be changed by you. Change their owner without mounting:

```bash
condatainer overlay chown env.img          # Everything owned by you
condatainer overlay chown env.img --root   # Owned by root, for --fakeroot
```

See [Fix Ownership](../guides/environments.md#fix-ownership).

## Cannot mount for writing

```text
cannot mount env.img for writing: fuse2fs 1.47.1 (/usr/bin/fuse2fs) has a bug that silently corrupts a file overwritten with shorter content. Run `condatainer update --libexec apptainer` to install a fixed version.
```

On a login node, outside any container:

```bash
condatainer update --libexec apptainer
```

### Why it is refused

- A `fuse2fs` older than 1.47.3 that is built on libfuse 3 does not shorten a file that is overwritten with less content. The new content is followed by leftover bytes of the old.  (`O_TRUNC`)
- A shell redirection (`cat small > file`), `cp` over an existing file, and installing an R package over an older version all do this.
- Some operating systems ship an affected `fuse2fs`.
