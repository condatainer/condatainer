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

- A blank overlay is made in place. There is nothing to stage.
- An overlay with packages is installed into a directory on local tmp, then packed into the destination.
  - Conda's many small writes are slow through an ext3 mount and cheap on a directory.
  - The stage is `<tmp>/overlay-create-*`, holding `upper/` and `work/`. `Setup` treats it as the writable layer.
  - The stage is not beside the destination, so a paired snapshot is looked up against the destination and mounted with it.
    - Otherwise the install would repeat what the snapshot already has instead of writing only the difference.
  - The pack is one mostly sequential pass, so it writes straight to the destination with no local copy of the image.
- The pack writes `<name>.partial`, locked, and renames it on success.
  - A failed or concurrent create never shows at the destination.
- A root-owned image is packed in a root-mapped user namespace, not chowned afterward.
  - A chown pass costs several times the pack. It stays as the fallback.
- The size limit is checked after the install, because the payload is unknown before it.
- `Resize` takes the same `sparse` choice and allocates by default.
- A non-sparse image is allocated before `mke2fs` runs, which is called with `-E nodiscard`.
  - Without it `mke2fs` discards the whole file and the image is sparse again.
  - A sparse image on shared storage can run out of space at the first write to a block, long after creation.
- Space is reserved with `fallocate`, and a failure other than "not supported" fails the create or resize.
  - NFS before 4.2 has no `fallocate`. Zeros are written instead, and creating takes longer.
  - Zeros go only where no data exists: a new file, or the part a resize adds. A formatted image is never overwritten.

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

## Locking

- Apptainer locks a mounted `.img` itself, so `exec` and `run` take no lock on it.
- An action that changes an image acquires the lock first, so a running container is never changed under.
  - `chown` and `overlay freeze` hold it for the whole operation. Freeze holds a shared lock, so a pinned overlay is still freezable.
  - `resize`, `check`, `remove` and `build --update` probe and release, since Apptainer takes the same lock and holding ours would collide.
- An image the caller cannot write is protected and never modified or removed. `chmod a-w` pins one, even against its owner.
  - The message tells the two apart. A pinned image says `chmod +w`. Someone else's names the owner, since its chmod is not the caller's to run.
- A failed attempt reports `ErrProtected`, `ErrInUse`, or a missing file. Callers may branch on the first two.
