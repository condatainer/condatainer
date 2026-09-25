# internal/libexec

The self-provisioned toolchain: one micromamba prefix in one of the four data-directory tiers.

- It always holds `micromamba`.
- It holds `squashfs-tools`, `squashfuse`, `fuse-overlayfs` and an ordinary (non-fakeroot) `apptainer` only when the system lacks them or they are asked for by name.
- It never contributes to an artifact's identity. It is host-local infrastructure, like the Apptainer binary itself.
- A conda or script build creates it with `micromamba` alone when no tier has one.
- A script build can call its `micromamba`, appended to `PATH` so anything the base provides wins.
  - That is a convenience, not an input. The version is not recorded, and a recipe is identified by its text.
- Nothing is provisioned automatically. An ordinary `exec`, `run` or build never triggers a first-time download.

## Placement

- `libexec/` sits beside `images/` in each tier, with the same nearest-read, furthest-write order.
- One copy serves the whole group, which matters more for a toolchain this size than for one image.
- The name is `libexec`, not `tools`. "Tool" already means other things here. `libexec` is the FHS term for executables invoked by other programs.

## The package table

- `packages` in `provision.go` is the one list of what a prefix may hold.
  - Each entry has the conda name, its binaries, which are checked, which one is version-checked, and the floor.
- `Provides`, `Installed`, `Versions`, `verifyToolchain` and every message naming a package derive from it.
- What a prefix holds is read from its `bin/`, not from code. A prefix with only `micromamba` is complete and valid.
- `micromamba` is the base. Its binary is what `Dir()` treats as "this tier is provisioned".

## Create and update

- `update --libexec` takes an exclusive lock on the tier, and on the prefix when one is live.
- **No prefix yet.** A standalone micromamba, downloaded outside the tier, creates the prefix at its final path.
  - A failed create removes the prefix, so nothing half-built is reported as provisioned.
- **A live prefix with `conda-meta/`.** Its own micromamba installs missing named packages, then updates.
  - It uses `update`, not `install`, because `install` leaves an already-satisfied spec at its current version.
- **A live prefix without `conda-meta/`.** Micromamba cannot track it and would forget every installed package.
  - It is removed and recreated with the tools that were in its `bin/`, plus any named.
- Afterwards it links `fusermount3` into `bin/`, cleans the package cache, shares the tree with the parent group and verifies.
- A failed update leaves what micromamba left. Re-running repairs it. There is no rollback copy.
- `micromamba self-update` is not used. It swaps the binary but leaves `conda-meta/` recording the old version.
- `conda-meta/` stays.
  - Removing it hides the prefix from IDE scanners, but `install` would then forget every earlier package record.
  - The cost is that the prefix can be offered as an environment.

## Nothing leaks out of the prefix

- `-r <prefix>` alone does not contain micromamba.
- It also sets `CONDA_PKGS_DIRS`, `XDG_CACHE_HOME` and a throwaway `HOME`.
  - Otherwise micromamba writes to `~/.mamba/pkgs` and `~/.cache/conda`, and registers the prefix in `~/.conda/environments.txt`.
  - No variable turns that registration off. On a shared tier the cache would land in whichever user ran the update.
- The throwaway `HOME` cannot live inside the prefix, since an existing directory there makes `create` refuse.
- `clean -a -f` is required. `-a` alone leaves the unpacked package directories, which micromamba considers in use.

## Baked-in absolute paths

- Micromamba patches absolute paths into installed binaries, using the `-p` prefix given to `create`.
  - `libfuse3` looks for `<prefix>/bin/fusermount3` before any `PATH` search.
- So the prefix is created at the path it keeps and never renamed.
- `bin/fusermount3` is linked to `../sbin/fusermount3` when the package installs it only in `sbin/`.
- The path given to `-p` is symlink-resolved, since that string is what gets baked in.

## Activation

- Accessors return a binary's absolute path. They do not activate an environment.
- The self-provisioned `apptainer` is the exception: it runs through a wrapper that sources the prefix's `etc/conda/activate.d/*.sh` first.
  - This is required. Apptainer hung indefinitely mounting a large overlay when invoked unactivated.
  - `CONDA_PREFIX` is set only per script, never exported, so it does not reach the containerized command.
- `conda-meta/`, `activate.d`, `deactivate.d` and `envvars` are left in place, since any of them could matter for the same reason.

## Resolved paths

- `Dir()` and everything built on it resolve symlinks before returning.
  - Callers embed the result as a literal path in a script run inside a container.
  - `$SCRATCH` on separate real storage is common on HPC. The logical path may not exist inside the container.
- A container-bound caller uses the absolute path as the command, not a `PATH` prepend.
  - Apptainer's `--env` replaces a variable outright, so a prepend would have to happen in the script text anyway.
  - An absolute path cannot be shadowed by a same-named tool earlier in the container's `PATH`. A prepend can, silently.
- A host-side caller needs only a runnable path, and gets one from `internal/toolpath`.
- `ApptainerPath` also adds `BinDir()` to `PATH` for the Apptainer subprocess, since Apptainer needs `unsquashfs` and `mksquashfs` for some operations.

## Naming a missing tool

- `Path(name)` returns a path only when the binary is installed in the provisioned `bin/`.
  - It does not say why a tool is missing. `Provides` says whether the table can install it, and `Installed` whether this tier has it.
- `ErrNotProvisioned` is the Go sentinel for a caller that fails before starting a container.
- `NotProvisionedMessage` is a plain string for a generated script or a host-side caller.
  - `toolpath.NotFoundMessage` re-exports it, so callers that already import `toolpath` need not import this package for a message.

## Standalone

- This package imports nothing under `internal/image`.
  - That lets `internal/toolpath` import it with no cycle. `internal/image/squashfs` needs `toolpath`, and `internal/image` must not depend on anything that depends back on its children.
- It does not know `PATH` or the FHS directories. `toolpath.Resolve` decides which binary wins.
  - It checks this package first, so the provisioned copy beats a same-named binary on `PATH`.
- The lock mechanism sits in `internal/utils` so both packages reach it without importing each other.

## Locking

- Two non-blocking locks, so a collision fails at once and nobody waits.
- `<prefix>/.lock`. A reader holds it shared for one apptainer call. `Update` takes it exclusive and refuses if a reader holds it.
- `<tier>/.libexec.lock`. `Update` holds it for its whole run, so two updates cannot interleave, including the first one, when no prefix exists yet.
- When a prefix without `conda-meta/` is recreated, the in-prefix lock is released before the removal. Unlinking an open file leaves a silly-rename on NFS.

## Verification

- `verifyToolchain` checks by output content, not exit status. `squashfuse` exits 254 for any argument-free call, though it prints its banner.
- `micromamba` prints only a bare version, so it is not content-checked.
- `mksquashfs` and `unsquashfs` take only `-version`. Everything else takes `--version`.
- Two floors, checked only for installed packages.
  - apptainer `>= 1.4`. Below that it cannot mount a zstd-compressed SquashFS.
  - squashfs-tools `>= 4.4`. Below that `mksquashfs` cannot write zstd, and `unsquashfs` has no `-offset`, which reading a SquashFS inside a SIF needs.
- The apptainer floor is independent of `runtime/apptainer.CheckZstdSupport`. Verifying a prefix must never touch that package's live-apptainer state.
- A prefix that fails verification is reported, and re-running the update repairs it. `Dir()` still treats it as provisioned, since its `micromamba` runs the repair.
