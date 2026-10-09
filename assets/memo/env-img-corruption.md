# Memo: env.img file corruption (unresolved)

A writable `env.img` held a package file whose contents did not match what was written. The cause is not found.

## Symptom

- The R package `presto` (installed from `immunogenomics/presto`) in one RStudio helper session failed to load in a later session: `unexpected symbol` while parsing `R/presto`.
- `R/presto` was the correct 1058-byte lazy-load loader followed by the stale tail of the earlier 62389-byte file.
- The inode on disk kept the old size and old block list, while the first data block held the new bytes.
- The inode's ctime was 4 seconds later than its mtime, yet it still showed the old size.
- The bad write happened about 46 minutes before the session was stopped, not at shutdown.
- In-session reads were correct. The damage showed only after the image was mounted again.

## Setup

- The image was a 10 GB ext3 file on a shared NFS 4.2 home, mounted through `fuse2fs` and `fuse-overlayfs` by Apptainer.
- Sessions ran on two different compute nodes, one at a time. The image lock held across them.
- The image was created on 2026-10-05 by the build that had just changed how allocation works.

## Findings

- **Sparse images.** Since 2026-09-30, creation ran `fallocate` before `mke2fs`, and `mke2fs` discards the whole file by default. Images that were meant to be fully allocated were sparse (about 1.8 GB used of 10 GB).
  - A sparse image on shared storage can run out of space at the first write to a block, long after creation.
  - A sparse image on local disk did not reproduce the damage, so this is not proven to be the cause.
- **Unclean mount.** A session on 2026-10-05 started with `Mounting unchecked fs, running e2fsck is recommended`. The stop before it was a walltime stop.
- **No fsck ever ran.** The image's "Last checked" time stayed at its creation time, although helper start-up is meant to run `e2fsck -p` on a not-clean image. Why it did not run is unexplained.
- **A leaked inode.** `e2fsck -fn` on the idle image later reported one error, `Inode bitmap differences: -N`. Inode N was a zero-length file created and deleted within one second in the session that began with the unclean mount. Its bitmap bit was never cleared.
  - This is another lost metadata update, and the image was marked clean afterwards.
- **Every helper stop times out.** Each helper log ends with `Terminating fuse-overlayfs after timeout`. `rserver` and `rsession` survive the TERM, because the script's `wait` exits without stopping them.
- **`fuse2fs` shutdown.** SIGTERM unmounts cleanly. SIGKILL leaves the image `not clean` and the next clean unmount hides that.
- **The journal is unused.** `fuse2fs` does not write the ext3 journal (journal start stays 0), so an unclean stop has nothing to replay.
- **Locks work across nodes** on the NFS versions checked (4.2 and 3).

## Not reproduced

- On local disk, with `fuse2fs` alone and with `fuse-overlayfs` on top:
  - clean unmount, SIGTERM and SIGKILL, with and without active writes;
  - a real `presto` install through an RStudio session, then SIGTERM;
  - eight forced reinstalls of `presto` in separate mounts;
  - an extra open handle while another process truncated and rewrote the file;
  - 300 rounds each of create-then-delete, delete-while-open and truncate-chmod-delete.
- Never tested: the image on NFS under a long RStudio session.

## Changes made

- Non-sparse creation keeps its allocation: `mke2fs` runs with `-E nodiscard`.
- Where `fallocate` is unsupported (for example NFS 3), zeros are written before formatting. Any other allocation failure fails the create or resize.
- `Resize` and `CheckIntegrity` hold the exclusive lock for the whole operation.
- A writable mount of an image whose superblock is not marked clean prints a warning that names `overlay fsck`. Nothing is repaired.

## If it recurs

1. Leave the image untouched. Do not reinstall or repair yet.
2. Copy it with `cp --sparse=always`.
3. Run `e2fsck -fn` on the idle image.
4. Run `debugfs -R "stat <path in upper/>"` on the bad file and note its size, mtime and ctime.
5. Record the node, the walltime and how the previous session ended.

## Testing notes

- Run stress tests on a scratch image, never on a project image.
- Inside another container, unset `APPTAINER_BIND` and `SINGULARITY_BIND` before a nested `condatainer exec`. A bind inherited from the outer container makes container creation fail.
