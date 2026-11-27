# internal/runtime/apptainer

## Which binary

- A run wants one thing: an apptainer `>= 1.4`.
  - Every artifact is zstd, which only apptainer `>= 1.4` can mount.
- The caller says what the launch needs, and `Resolve` returns the binary. The launch carries it in `Bin`. `exec.Prepare` asks once fakeroot is final.
  - `Overlays`: the launch mounts condatainer's overlays. A host apptainer is refused below the floor, and the refusal names the way out, since a later failure inside Apptainer's mount step would not.
  - `Fakeroot`: the launch uses `--fakeroot`. An explicit flag, a fakeroot turned on for a writable `.img`, and a `.def` build all need it.
- The binary is the `host_apptainer` when set, else libexec's. A `host_apptainer` that cannot be run is `ErrHostApptainerUnusable`, never replaced by libexec's, since the user chose it. `Selected` names it and its source without running anything, for the startup warning and the debug line. No apptainer on `PATH` is ever used, so which one runs is always something the user chose.
  - Inside a container only libexec's is used. The host's cannot be assumed to be there, or to be configured the way the host's is.
  - With none bound, the message says to install the toolchain on the host and start the container again.
- libexec's apptainer is the one that ships its own `fuse2fs`, `squashfuse_ll`, `fuse-overlayfs` and fakeroot. A run behaves the same on every host that has it.
  - The host's apptainer mounts a writable `.img` with the `fuse2fs` in its own tool directory, else on `PATH`. The guard checks that one, not the toolchain's.
  - A refusal says to unset `host_apptainer`, since the toolchain's apptainer ships a fixed one.
- `Fakeroot` runs inside a container as it does on the host.
  - libexec's apptainer is not setuid. It fakeroots in a root-mapped user namespace, with its bundled `fakeroot-sysv` for the owner changes that namespace cannot make.
  - `fakeroot-sysv` is a glibc program, so every fakeroot launch on a musl image such as Alpine fails: a run, and a `.def` build at `%post`. Apptainer skips it only for a user with a subuid range, which a cluster rarely gives.
  - That is accepted as a limit. A musl image runs without fakeroot, and a `.def` build uses a glibc base.
  - A nested launch needs only the user namespace a plain nested launch already needs, so a site that allows one allows the other.
  - A `.def` build running `apt`, a read-only launch, and a writable `.img` made with `overlay create --fakeroot` all ran nested.
  - A writable `.img` owned by the user fails with fakeroot on the host and nested alike, since its `upper` must be owned by root.
- A `.def` build needs `Fakeroot` and no `Overlays`: the output is a sandbox, and `#DEP:` is data-only, so no zstd artifact is mounted during the build.
  - Packing the sandbox runs `mksquashfs` on the host, never in a container.
- Every launch with `Fakeroot` has the same fakeroot rules, in one place.
  - The toolchain's `lib/apptainer/bin` goes first on `PATH`, carried in `Bin.Env`, so a host apptainer binds the bundle's fakeroot.
    - An unprivileged apptainer binds the first `fakeroot-sysv` it finds, and the host's is built for the host's glibc. An image with an older glibc fails to start its daemon, for a build and for an exec alike.
  - An apptainer searches its own tool directory before `PATH`, so a non-setuid host apptainer that ships a `fakeroot-sysv` there cannot be redirected.
    - Its `buildcfg` is read on each such launch. Such a host apptainer is replaced by libexec's, when installed, with one warning.
    - A setuid install runs no fakeroot, so it is kept.
  - A build keeps its stderr, through a writer that is not a file, so a failure can be read. The loader's refusal of the fakeroot daemon or library is `ErrFakerootTooNew`, with the way out.
    - Without the toolchain it says to run `update --libexec`. With it, to unset `host_apptainer`.

## Refused user namespaces

- A launch the system refused a user namespace for is `ErrUsernsBlocked`, not an `ApptainerError`.
  - The caller then gets the message instead of the container command's exit status.
  - Only `exec` and `build` make a namespace, so only they are checked.
- The refusal is read from apptainer's output. Three messages mean it: `setgroups`, `mount namespace requires privileges` and, for a launch with a FUSE mount, `Failed to create container process: Operation not permitted`.
  - The last is vague, so it counts only while AppArmor's restriction is on.
- A launch with stderr on a terminal leaves no output to read.
  - A failed launch under the restriction is then settled by launching the same apptainer on an empty directory. The refusal does not have one exit status, so none is assumed.
  - An apptainer that is allowed gets past the namespace and fails on the missing command. One that is refused fails at the namespace.
- The probe runs only after a failed launch, and its answer is not kept. A profile an administrator installs applies at once.
- The message names the cause only when a sysctl shows one: AppArmor's restriction, `user.max_user_namespaces` at 0, or `kernel.unprivileged_userns_clone` at 0. Another cause, such as SELinux or a container runtime's filter, gets the generic message.
- A refused toolchain apptainer is told about an apptainer on `PATH` only when that one proves it works.
  - It must be `>= 1.4` and run `/bin/true` in a sandbox with the host's system directories bound in. A launch that was merely not refused is not enough.
  - The suggestion is the `host_apptainer` command. An apptainer on `PATH` is never used without it.
  - A refused `host_apptainer` gets none, since it was the user's own choice.
- A sysctl does not decide that a binary is blocked. It says a limit is on, not whether this binary is allowed. It only gates the vague message and picks the advice.

## Writable images

- A launch that mounts a writable `.img` is refused when the `fuse2fs` it would use is older than 1.47.3 and linked against libfuse 3.
  - That `fuse2fs` keeps the old size of a file that is rewritten shorter. The image is damaged with no error, and `e2fsck` reports it clean.
- libexec's apptainer is not checked. It mounts with the `fuse2fs` of its own bundle, which is only released when that `fuse2fs` passes the smoke test.
- A `fuse2fs` whose version or libraries cannot be read never blocks a launch.

## The binary a build records

- `internal/build` records the version of the apptainer a build ran under, but must not decide that itself. Resolving separately could name a different binary than the one the build used.
- A build keeps the `Bin` its container step ran under. `exec.RunWithBin` returns it, and a `.def` build keeps the one it resolved.
- Nothing is remembered in the package, so two builds in one process cannot record each other's apptainer.
- A build with no container step has no `Bin`, and its apptainer version is recorded as unrecorded.

## Helpers

- libexec's apptainer finds `mksquashfs`, `unsquashfs`, `squashfuse_ll`, `fuse2fs`, `fuse-overlayfs` and `fakeroot-sysv` in its own tool directory.
- The host's apptainer looks in its own tool directory and `PATH`.

## Launch decisions

- Every launch passes `-q`, so Apptainer's INFO lines are hidden. Warnings and errors still print.
  - `--debug` leaves it off, to show what Apptainer does.
- CondaTainer never sets `--workdir`, and leaves `APPTAINER_WORKDIR` as the environment has it.
  - A container sees the host's `/tmp` and every temporary directory the job names, at their own paths. `TMPDIR` is kept, so a program that follows it writes to the scheduler's node-local scratch.
  - A private `/tmp` and `/var/tmp` per container hides the host's. That is a site's decision, made by exporting `APPTAINER_WORKDIR`.
- Build operations unset all four bind variables (`APPTAINER_BIND`, `SINGULARITY_BIND` and the two `*_BINDPATH`), to avoid `%post` mount conflicts.
- A launch with `NoInheritedBinds` unsets them too. The freeze job binds all it needs, and its bare root has no mount point for a file an outer container binds.
- A launch from a container removes the inherited bind entries it passes itself, from all four bind variables.
  - The outer apptainer exports its binds under those variables, so a repeat is a duplicate-mount warning.
  - Entries it does not pass, such as a site's, are kept.
- Definition builds pass `--fix-perms`, so an OCI base's owner-unreadable files and no-write directories are packed with their content and removable at cleanup.
- Cancellation sends SIGTERM, then SIGKILL after `ExecOptions.StopGrace` (5s when unset).
- Environment values go as `APPTAINERENV_*`, not `--env`, which is parsed with CSV rules and also exposes values in `ps`.
