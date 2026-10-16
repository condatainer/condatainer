# internal/libexec

The self-provisioned toolchain: one directory holding micromamba and the Apptainer bundle.

- `micromamba` is a standalone binary, `bin/micromamba`.
- The Apptainer bundle is one release archive of `condatainer/libexec`.
  - It holds `apptainer`, `mksquashfs`, `unsquashfs`, `squashfuse_ll`, `fuse-overlayfs`, `fusermount3`, `fuse2fs`, `mke2fs`, `e2fsck`, `tune2fs`, `debugfs`, `resize2fs` and a fakeroot.
  - It is built for the oldest glibc CondaTainer supports, so one archive per architecture runs on every host.
- It never contributes to an artifact's identity. It is host-local infrastructure, like the Apptainer binary itself.
- A conda or script build installs `micromamba` alone when none is installed.
- A script build can call its `micromamba`, appended to `PATH` so anything the base provides wins.
  - That is a convenience, not an input. The version is not recorded, and a recipe is identified by its text.
- Nothing is installed automatically. An ordinary `exec`, `run` or build never triggers a first-time download.

## Why a bundle

- A host's own `fuse2fs`, `squashfuse` and `fuse-overlayfs` decide how an image behaves. Hosts differ, and an old `fuse2fs` on libfuse 3 silently keeps the old size of a file that is rewritten shorter.
- The bundle carries the versions Apptainer was released with, so a run behaves the same everywhere.
- Micromamba is separate. It is not part of Apptainer, has its own releases, and is needed even where the bundle is not.

## Placement

- There is one toolchain directory, never searched across tiers: `CNT_LIBEXEC`, else `<root>/libexec`, else the user data directory's `libexec`.
  - It belongs to the install, like the Apptainer binary. No other layer can shadow it.
  - With no root, it falls back to the user data directory.
- `CNT_LIBEXEC` is a variable only, with no config key, so no config layer can move it.
- A container binds the directory whole, at its real path.
- The name is `libexec`, not `tools`. "Tool" already means other things here. `libexec` is the FHS term for executables invoked by other programs.
- The directory counts as installed when it has `bin/micromamba` or `MANIFEST.json`. Either one alone is a valid toolchain.

## Install and update

- `update --libexec` takes the update lock first. Only someone who can write the directory can update it.
- **Bundle.** The newest release of `condatainer/libexec` is looked up, and the archive for this architecture is downloaded.
  - Its sha256 is checked before anything is unpacked.
  - It is unpacked into `.staging/` inside the toolchain, so renaming a file into place never crosses a filesystem.
  - Apptainer and squashfs-tools are run from there and checked against their version floors before any installed file changes.
  - Then each file is renamed over the old one, and `MANIFEST.json` goes last.
- **Replacing files.** `MANIFEST.json` lists every file of the installed bundle.
  - A file of the previous bundle that the new one lacks is removed.
  - A file the manifest never listed is left alone, including `bin/micromamba`.
  - A running process keeps its old inode when a file is renamed over it.
- A failed update leaves the previous manifest. Running it again repairs the install. There is no rollback copy.
- **Micromamba.** The standalone release for this platform is downloaded and checked beside `bin/micromamba`, then renamed over it.
- A directory holding anything else is refused, never written into, so a mistyped `CNT_LIBEXEC` loses nothing.

## Choosing a version

- A package is named `name` or `name@version`: `apptainer@1.5.4`, `micromamba@2.0`.
  - Without a version, the newest release is installed.
  - A partial version picks the newest release it matches: `1.5` is the newest `1.5.x`, and does not match `1.50.0`.
  - A micromamba release tag carries a build number (`2.8.1-1`). Of two builds of one version, the higher wins.
- The version asked for is the version installed, so naming an older one is a downgrade. There is no stored pin: the next update without a version moves to the newest.
- A version is looked up in the releases listing, which holds the newest few hundred. A release older than that cannot be named.
- Pre-releases and drafts are never chosen.

## The archive is untrusted until checked

- Only directories, regular files and symlinks are accepted.
- A path with `..`, an absolute path, and a symlink that points outside the tree are refused.
- Files get mode 755 when the archive marks them executable and 644 otherwise, and new directories get 755, whatever the umask.
  - Another user can run the toolchain whatever the umask of whoever installed it.
  - A file or directory also gets the group bits of the directory it lands in, so a group-writable install stays group-writable.
- The same applies to micromamba: its binary is checked against the sha256 published beside it before it replaces the installed one.
- There is no signature check. The sha256 comes from the same release as the archive, so it detects a bad download, not a bad release.

## Resolved paths

- `Dir()` and everything built on it resolve symlinks before returning.
  - Callers embed the result as a literal path in a script run inside a container.
  - `$SCRATCH` on separate real storage is common on HPC. The logical path may not exist inside the container.
- A container-bound caller uses the absolute path as the command, not a `PATH` prepend.
  - Apptainer's `--env` replaces a variable outright, so a prepend would have to happen in the script text anyway.
  - An absolute path cannot be shadowed by a same-named tool earlier in the container's `PATH`. A prepend can, silently.
- A host-side caller needs only a runnable path, and gets one from `internal/toolpath`.
- `Path(name)` looks in `bin/`, then in `lib/apptainer/bin/`, where the bundle keeps `squashfuse_ll` and `fuse-overlayfs`.
  - Apptainer finds its own helpers beside itself.
- The bundle is relocatable. Apptainer finds its configuration and helpers from its own location.

## Naming a missing tool

- `Path(name)` returns a path only when the binary is installed in the toolchain.
  - It does not say why a tool is missing. `Installed` says whether the toolchain has it.
- `ErrNotProvisioned` is the Go sentinel for a caller that fails before starting a container.
- `NotProvisionedMessage` is a plain string for a generated script or a host-side caller.
  - It names `update --libexec apptainer` for every bundle tool and `update --libexec micromamba` for micromamba.
  - `toolpath.NotFoundMessage` re-exports it, so callers that already import `toolpath` need not import this package for a message.

## Standalone

- This package imports nothing under `internal/image`.
  - That lets `internal/toolpath` import it with no cycle. `internal/image/squashfs` needs `toolpath`, and `internal/image` must not depend on anything that depends back on its children.
- It does not know `PATH` or the FHS directories. `toolpath.Resolve` decides which binary wins.
  - It checks this package first, so the installed copy beats a same-named binary on `PATH`.
- The lock mechanism sits in `internal/utils` so both packages reach it without importing each other.

## Locking

- One non-blocking lock, `<toolchain>/.libexec.lock`. `Update` holds it for its whole run, so two updates cannot interleave.
  - It lives inside the toolchain, so it needs no write access anywhere else. No manifest lists it.
  - A lock that cannot be created or opened for writing is reported as "not writable", never as another update.
- A run takes no lock on the toolchain.
  - A reader may be unable to write the toolchain directory.
  - An update renames files over the old ones, so a running process keeps its old inodes.
  - A reader lock would also block every update while anything runs.
  - The cost is a tool started mid-update that briefly sees a mix of old and new files, which fails loudly.

## Version floors

- Checked on the unpacked bundle before it is installed, and on a host binary before it is used.
  - apptainer `>= 1.4`. Below that it cannot mount a zstd-compressed SquashFS.
  - squashfs-tools `>= 4.4`. Below that `mksquashfs` cannot write zstd, and `unsquashfs` has no `-offset`, which reading a SquashFS inside a SIF needs.
- The floor is read from the binary's own output, not its exit status. `unsquashfs` prints its version and exits 1.
- The apptainer floor is independent of `runtime/apptainer.CheckZstdSupport`. Checking a bundle must never touch that package's live-apptainer state.
