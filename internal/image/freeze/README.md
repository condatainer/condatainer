# internal/image/freeze

## No container

- Freeze and unfreeze run on the host with `mksquashfs`, `mke2fs`, `debugfs` and `unsquashfs`. No Apptainer and no base image.
- Reading an image without mounting it needs nothing more.
- `Unfreeze` needs a live mount, because `mke2fs -d` finds whiteout device nodes through `stat()`. An unprivileged `unsquashfs -d` cannot create them at all.
- That mount is `MountedRun`. `internal/build` reuses it to read a `.sif`.

## The mount

- A bare unprivileged FUSE mount can be refused, because `fusermount3` normally escalates through a setuid-root binary.
  - That path is unavailable on a `nosuid` filesystem, or where the binary is not setuid, or where an admin disabled it.
- `unshare --mount --user --map-root-user` does not use setuid at all.
  - It maps namespace-uid 0 to the caller, and the kernel grants capability inside a namespace nobody else can see.
  - It works from a login shell or from inside a container, since each call creates a fresh namespace.
- The mount is ended by killing the foreground FUSE process, never `umount` or `fusermount`.
  - Those are setuid-root binaries, and the owning uid has no mapping in the namespace. Some kernels refuse to exec them.
  - The namespace, mount included, disappears when nothing runs in it, so a killed run leaks nothing.

## The sentinel

- `MountedRun` re-execs into a hidden `_mount_sentinel` instead of calling `unshare` directly.
- `Pdeathsig` would let the kernel tell a process its parent died, but entering the user namespace clears it.
  - Becoming root-in-namespace is the same kind of credential change that clears it across a setuid exec.
  - This was confirmed on the target kernels.
- The sentinel stays outside the namespace and never escalates, so its `Pdeathsig` keeps working.
- `unshare`, bash and FUSE join its process group, so one group kill takes the whole tree down.
- The leak window is narrowed, not closed. Killing the sentinel directly still orphans the tree.
  - What has to die is now a tiny short-lived process, not condatainer for the whole mount.

## Unfreeze ownership

- The namespace maps exactly one identity, uid 0.
- A `squashfuse -o uid=<real-uid>` override is clamped to the overflow uid, since it is not a mapped value.
- Widening the mapping needs `newuidmap` and `/etc/subuid` delegation, which an arbitrary cluster user may lack.
- So `buildImage` mounts with no override, and `mke2fs -d` bakes in the `-all-root` ownership.
- `ext3.ChownRecursively` fixes it afterward on the plain host. `overlay chown` uses the same tool.

## Freezing over a snapshot

- A thin `.img` holds only what changed since the snapshot beneath it. Freezing it alone would drop the snapshot's content.
- So a freeze packs the two layered, and the artifact is self-contained.
  - It holds the snapshot's files, the `.img`'s edits over them, and both sets of deletions.
  - It is the same whether the artifact replaces the snapshot or goes to an explicit destination.
- Both are mounted read-only as lower layers of a `fuse-overlayfs` union, with a scratch upper. Neither is written.
  - The union applies edits, deletions, opaque directories, type changes and symlinks exactly as a mount does, so none is reimplemented.
- The union swallows every char `0:0` node, in any layer. Whiteouts are injected as pseudo-files instead.
  - The `.img`'s own, as the staged copy route does.
  - The snapshot's, read from its archive listing. Its whiteouts hide base files, and dropping them would bring those back.
  - A snapshot whiteout is skipped when the `.img` recreates the path, or hides a directory above it. A pseudo-file cannot sit under a missing directory.
- A deletion stays a whiteout even where nothing beneath it needs one, since the base may hold the same path.
- The snapshot's own `.cnt` metadata is not carried. The new archive gets its own.
- The manifest records `fuse-overlayfs`'s version with the other tools, since it decides the merged content.
- The extra FUSE processes run in the same namespace as the mount, and are killed when the pack ends. Otherwise the namespace would never end.
- There is no staged-copy route for it. `--use-tmp` is refused.

