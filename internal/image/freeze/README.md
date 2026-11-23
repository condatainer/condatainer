# internal/image/freeze

## No container

- Freeze and unfreeze run on the host with `mksquashfs`, `mke2fs`, `debugfs` and `unsquashfs`. No base image.
- Reading an image without mounting it needs nothing more.
- `Unfreeze` needs a live mount, because `mke2fs -d` finds whiteout device nodes through `stat()`. An unprivileged `unsquashfs -d` cannot create them at all.
- Packing an image in place and reading a `.sif` need a mount too.
- Every mount is `MountedRun`, which runs them in one apptainer job. `internal/build` reuses it to read a `.sif`.

## The apptainer job

- A bare unprivileged FUSE mount can be refused, because `fusermount3` normally escalates through a setuid-root binary.
  - That path is unavailable on a `nosuid` filesystem, or where the binary is not setuid, or where an admin disabled it.
- `unshare` is not used. Where AppArmor restricts unprivileged user namespaces, a program with no profile is refused one, and `unshare` has none.
  - An apptainer has a profile: the Apptainer package installs its own, and an administrator adds one for the libexec apptainer.
  - So a site allows one program, not several.
- A job has no mount capability. A FUSE server started from its script fails, so every mount is declared up front, in order, and the script only reads them.
  - Apptainer makes the mounts itself, in the namespace it creates. The user maps to the user, with no root map.
  - A later mount can name an earlier mountpoint, which is how the union layers the snapshot and the image.
- The mounts are `--fusemount`, never `--overlay`. An overlay merges the artifact into `/`, so the root's own files would be read with it.
- The root is a bare sandbox directory made for the job. The host's system directories, the toolchain, the directories on `PATH` and the paths the caller names are bound into it.
  - A file is bound as its directory. A path that does not exist yet stands for its parent.
  - `/etc/passwd` and `/etc/group` are the exception and are bound as files. Binding `/etc` brings in the host's `resolv.conf`, which can be a link into `/run`, and the job's root has none.
- A `--fusemount` specification is split on spaces and commas, and quoting does not help.
  - A path with either is reached through a link in the job's directory.
  - A bind cannot carry a comma or a colon, so a path with one is refused.
- Inside a container the job runs through the libexec apptainer that every container binds, never the host's.
- Apptainer's warnings are dropped, since the root is a bare directory and it complains about what it lacks. Errors still print.
- The mounts end with the job. Nothing is unmounted with `umount` or `fusermount`, so a killed run leaks no mount.
- A write-protected `.img` mounts read-only on its own. The job holds no capability that skips permission checks, so `fuse2fs` decides by the mode bits.

## Reading files in the job

- Without a root map, a file owned by another uid shows as the overflow uid.
  - `fuse2fs` checks permissions as the user, so it refuses a file whose owner is another uid and whose mode excludes the rest. The image is mounted with `-o fakeroot` to check as root, when its `fuse2fs` has the option.
  - The other mounts read every file already: `squashfuse` and `fuse-overlayfs` do not enforce the mode bits for the user who mounted them.
- `mksquashfs` packs a file it cannot read as an empty one and exits 0. The pack script reads its messages and fails on `creating empty file`.
- The archive is packed `-all-root`, so the owner it records does not change.

## The job's command

- The job starts through the hidden `_mount_job`, a process that never enters a namespace.
- `Pdeathsig` would let the kernel tell a process its parent died, but the apptainer drops it when it enters its namespace.
  - Killing only the parent of a plain launch leaves the FUSE servers and the work running.
  - `_mount_job` keeps its own `Pdeathsig`, and stops the apptainer on it. That ends the FUSE servers and the work.
- Cancelling kills the command's process group, which takes the apptainer with it.
- The leak window is narrowed, not closed. Killing `_mount_job` directly still orphans the job.
  - What has to die is a tiny short-lived process, not condatainer for the whole mount.

## Unfreeze ownership

- The job maps the user to the user, so a file shows as the overflow uid unless the FUSE server is told another.
- `squashfuse_ll` is mounted with the owner's uid and gid, so `mke2fs -d` records them and no pass over the image follows.
  - The whiteout nodes take the owner too.
- An owner other than the caller's is applied after the build with `ext3.ChownRecursively`, which `overlay chown` also uses.

## Freezing over a snapshot

- A thin `.img` holds only what changed since the snapshot beneath it. Freezing it alone would drop the snapshot's content.
- So a freeze packs the two layered, and the artifact is self-contained.
  - It holds the snapshot's files, the `.img`'s edits over them, and both sets of deletions.
  - It is the same whether the artifact replaces the snapshot or goes to an explicit destination.
- Both are mounted read-only as lower layers of a `fuse-overlayfs` union, with a scratch upper. Neither is written.
  - The union applies edits, deletions, opaque directories, type changes and symlinks exactly as a mount does, so none is reimplemented.
- The union reports every file as owned by root, with `squash_to_root`. The archive is packed `-all-root`, so the recorded owner is the same.
- The union swallows every char `0:0` node, in any layer. Whiteouts are injected as pseudo-files instead.
  - The `.img`'s own, including the char `0:0` nodes it holds.
  - The snapshot's, read from its archive listing. Its whiteouts hide base files, and dropping them would bring those back.
  - A snapshot whiteout is skipped when the `.img` recreates the path, or hides a directory above it. A pseudo-file cannot sit under a missing directory.
- A deletion stays a whiteout even where nothing beneath it needs one, since the base may hold the same path.
- The snapshot's own `.cnt` metadata is not carried. The new archive gets its own.
- The manifest records `fuse-overlayfs`'s version with the other tools, since it decides the merged content.
- The extra FUSE processes run in the same namespace as the mount, and are killed when the pack ends. Otherwise the namespace would never end.

