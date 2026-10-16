# Memo: a rewritten file keeps its old size in env.img

A file in a writable `env.img` that is rewritten shorter keeps its old size on disk. The new bytes are followed by the tail of the old content.

## Symptom

- An R package failed to load after the image was mounted again: `unexpected symbol` while parsing `R/<pkg>`.
- `R/<pkg>` was the correct 1058-byte lazy-load loader followed by the stale tail of the earlier source file.
- The inode kept the old size and old block list, while the first data block held the new bytes.
- The inode's ctime was later than its mtime, yet it still showed the old size.
- The write happened at install time, not at shutdown.
- In a mount that goes through `fuse-overlayfs`, reads in the same session show the new size. The damage appears after the next mount.
- `e2fsck` reports no error for it, because the metadata is consistent.
- Every source-built R package with R code was affected. Binary installs were not.

## Cause

- `fuse2fs` up to 1.47.2 ignores `O_TRUNC` in `open`. Version 1.47.3 handles it.
- Any open that truncates is affected: shell `>`, `cp` over an existing file, R `file.copy`, editors.
- A direct `fuse2fs` mount with no overlay and no container reproduces it. The file keeps its old size even within the session.
- libfuse3 requests atomic `O_TRUNC` by default, so the kernel hands the flag to `open` and the filesystem must truncate.
- libfuse2 requests it only with `-o atomic_o_trunc`, so the kernel sends a separate truncate first.
- `fuse2fs` 1.47.1 built against libfuse3 is affected.
- `fuse2fs` 1.47.0 built against libfuse2 is not.
- A cluster that provides `fuse2fs` 1.47.0 on libfuse2 through a software stack truncates correctly end to end through Apptainer.
- Clusters whose OS packages provide `fuse2fs` 1.47.1 on libfuse3 lose the truncate on every node checked.
- The storage filesystem, R, `fuse-overlayfs`, session length and walltime kills are not involved.

## Reproduction

1. Mount an ext3 image with an affected `fuse2fs`.
2. Write a 62383-byte file, then run `cat small > file` with a 1058-byte `small`.
3. The size stays 62383.

The same steps with `fuse2fs` 1.47.0 on libfuse2 give 1058.

## Findings that are not the cause

- **Sparse images.** Creation ran `fallocate` before `mke2fs`, and `mke2fs` discards the whole file by default. Images meant to be fully allocated were sparse.
- **Unclean mount.** A session that follows a walltime stop starts with `Mounting unchecked fs, running e2fsck is recommended`.
- **No fsck ran.** The image's "Last checked" time stayed at its creation time, although helper start-up is meant to run `e2fsck -p` on a not-clean image. Why it did not run is unexplained.
- **Every helper stop times out.** Each helper log ends with `Terminating fuse-overlayfs after timeout`. `rserver` and `rsession` survive the TERM, because the script's `wait` exits without stopping them.
- **`fuse2fs` shutdown.** SIGTERM unmounts cleanly. SIGKILL leaves the image `not clean`, and the next clean unmount hides that.
- **The journal is unused.** `fuse2fs` does not write the ext3 journal, so an unclean stop has nothing to replay.
- **Copies of a mounted image.** A copy taken while the image is mounted reads as `not clean` and can show a leaked inode bit. Copy images only when idle.
- **Locks work across nodes** on the NFS versions checked (4.2 and 3).

## Changes made

- Non-sparse creation keeps its allocation: `mke2fs` runs with `-E nodiscard`.
- Where `fallocate` is unsupported (for example NFS 3), zeros are written before formatting. Any other allocation failure fails the create or resize.
- `Resize` and `CheckIntegrity` hold the exclusive lock for the whole operation.
- A writable mount of an image whose superblock is not marked clean prints a warning that names `overlay fsck`. Nothing is repaired.
- libexec holds an Apptainer bundle with `fuse2fs` 1.47.4, downloaded by `update --libexec apptainer`. Apptainer mounts a writable `.img` with the `fuse2fs` beside it.
- A writable mount is refused when the `fuse2fs` it would use is older than 1.47.3 and linked against libfuse 3. The message names the update command.

## Handling a damaged image

1. Leave the image untouched.
2. Copy it with `cp --sparse=always` while it is idle.
3. Run `e2fsck -fn` on the copy. A clean result does not rule out damage.
4. Run `debugfs -R "stat <path in upper/>"` on a suspect file and compare its size with the real content length.
5. For R, list every `R/<pkg>` in the library. A source-built package whose size is larger than the loader size of a binary-installed one is damaged.
6. Reinstall the damaged packages. Other files rewritten shorter in the same image may be damaged too.

## Testing notes

- Run stress tests on a scratch image, never on a project image.
- Inside another container, unset `APPTAINER_BIND` and `SINGULARITY_BIND` before a nested `condatainer exec`. A bind inherited from the outer container makes container creation fail.
- A Slurm batch script runs from a copy in the spool directory. Never derive paths from `$0`.
- Slurm node names can be fully qualified while the node reports a short hostname. Pass the name Slurm registers to `--nodelist`.
