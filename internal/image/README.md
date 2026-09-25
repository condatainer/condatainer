# internal/image

File operations on overlay images: create, resize, chown, check, lock, read a path out of an archive, and find what is installed.

- It knows nothing about manifests, keys or what `/.cnt` means.
- `internal/artifact` reads through here. The dependency is one way.

## Overlay index

- `ScanOverlays` is the single walk of the image search paths.
- It keeps every copy of a name in search order, so the shadowed ones stay reachable.
  - `remove --layer` needs a copy the priority map would have hidden.
- Two things vary by caller, so they are options and not separate scanners.
  - `Aliases` adds a bare-name key for each `<base>/<name>` image, so `run -o build-essential` finds `ubuntu24/build-essential`.
    - Runtime resolution wants it. A `#DEP:` or `remove` names an image exactly and must not hit it.
  - An unreadable directory returns an error alongside a usable map.
    - Container launch treats it as fatal, since a shorter list would silently drop an overlay.
    - CLI listings warn and carry on.
- A `.sif` is never an overlay. It is the container root.

## What Apptainer treats as an overlay

- A writable overlay is an ext3 image with an `upper/` and a `work/` directory at its root.
  - `upper/` is the layer the container writes to. `work/` is overlayfs scratch space.
- Both are created owned by the requesting user's uid and gid.
  - The owner is the user the container writes as.
  - uid and gid 0 make a fakeroot overlay, which needs `--fakeroot`.
- Without `--fakeroot`, a root-owned `upper/` fails to mount with "setup of overlay upper dir failed: ... is not writable: permission denied".
  - `chown` fixes it by giving the tree to the current user.
- `chown` rewrites the owner of `upper/`, `work/` and `work/work`, and of everything under `upper/`.
- The owner of `upper/` also tells whether `--fakeroot` is turned on automatically.
- A `.sqf` is a read-only overlay as it stands.
  - Apptainer mounts it with `squashfuse_ll -o`, which overrides the archive's inner uid and gid.
  - So it has none of the `.img` ownership problem, and it cannot be written.
- A `.sif` is the container root and never an overlay.

## Creation

- An overlay is built sparse in the tmp directory first, on local storage with fast random I/O, then moved to its destination.
  - The caller can run more work, such as conda init, on the tmp image before the move.
- `MoveOverlayCopied` renames on one filesystem. Across filesystems it copies.
  - `sparse=false` writes every byte, so the destination is fully allocated.
  - `sparse=true` preserves holes.
- `--no-tmp` builds directly when the target is already on fast local storage.
- `Resize` takes the same `sparse` choice and allocates by default. Growing leaves a hole whatever the image was built as.

## External tools

- Every call to `debugfs`, `e2fsck`, `tune2fs`, `mke2fs`, `resize2fs`, `fuse2fs`, `mksquashfs`, `unsquashfs` and `squashfuse` goes through `toolpath.Resolve`.
  - A bare name in `exec.Command` is `PATH`-only with no fallback.
- e2fsprogs is host-only and never provisioned into libexec. It is on practically every Linux host, so bundling it would duplicate it.
- `fuse2fs` has no source condatainer can bundle either. It is a documented prerequisite.
- `dd`, `fallocate` and `unshare` are excluded. They are core-OS tools more universal than e2fsprogs.
- Finding a tool is `internal/toolpath`'s job, not this package's. `internal/conda` needs the same lookup and is not under `internal/image`.
- What stays in `tool` is interpreting an archive read: the failure hint text and the `ErrFileNotFound`, `ErrUnreadable` and `ErrCorrupt` sentinels.

## Archive reads

- `ExtractDir` decides by its output, not the exit code.
  - `unsquashfs` exits 0 when nothing matched.
  - It exits non-zero over warnings about files it extracted fine. An unprivileged user cannot restore `security.*` xattrs.
  - So it passes `-no-xattrs` and then stats the extracted path.
- `squashfs.PathExists` lists with `unsquashfs -lc -d ""` and requires an exact match on `/entry` or a prefix match on `/entry/`.
  - `-lc` lists only files and empty directories, so a populated directory shows up through its children.
  - unsquashfs 4.4 prints banner lines even when nothing matches, so "there was output" is not a signal.
- `ext3.crossFsCopy` shells out to `cp` and uses `cmd.Start` with a Wait goroutine, so a long copy stays cancellable.
  - On Lustre or NFS a `cp` in uninterruptible I/O sleep ignores SIGKILL until the I/O resolves.
  - `CombinedOutput` would block behind it.

## Locking

- Apptainer locks a mounted `.img` itself, so `exec` and `run` take no lock on it.
- An action that changes an image acquires the lock first, so a running container is never changed under.
  - `chown` and `overlay freeze` hold it for the whole operation. Freeze holds a shared lock, so a pinned overlay is still freezable.
  - `resize`, `check`, `remove` and `build --update` probe and release, since Apptainer takes the same lock and holding ours would collide.
- An image with its write bit clear is protected and never modified or removed, even for its owner. `chmod a-w` pins an artifact.
- A failed attempt reports `ErrProtected`, `ErrInUse`, or a missing file. Callers may branch on the first two.
