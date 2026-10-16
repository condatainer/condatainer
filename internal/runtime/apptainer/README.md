# internal/runtime/apptainer

## Which binary

- A run wants one thing: an apptainer `>= 1.4`.
  - Every artifact is zstd, which only apptainer `>= 1.4` can mount. Singularity cannot.
- The caller resolves the binary it needs, and the launch carries it in `Bin`. `exec.Prepare` picks once fakeroot is final.
- `Normal` is for an ordinary launch.
  - libexec's apptainer when installed, so installing one is how a user chooses it over the host's.
  - Otherwise the host's, refused below the floor. The refusal names the way out, since a later failure inside Apptainer's mount step would not.
  - Inside a container that advice cannot be followed, so the message points at `nested_run`.
- libexec's apptainer is the one that ships its own `fuse2fs`, `squashfuse_ll`, `fuse-overlayfs` and fakeroot. A run behaves the same on every host that has it.
  - The host's apptainer mounts a writable `.img` with whatever `fuse2fs` the host has.
- `Fakeroot` is `Normal`'s, refused at once inside a container with `ErrNeedsHost`.
  - libexec's apptainer is not setuid. It fakeroots in a root-mapped user namespace, with its bundled `fakeroot-sysv` for the owner changes that namespace cannot make.
- `ForBuild` is for a `.def` build's own `apptainer build --fakeroot`.
  - The host's with no floor, else libexec's. The output is a sandbox, and `#DEP:` is data-only, so no zstd artifact is mounted during the build.
  - Packing the sandbox runs `mksquashfs` on the host, never in a container.
  - It refuses at once inside a container with `ErrNeedsHost`.

## Writable images

- A launch that mounts a writable `.img` is refused when the `fuse2fs` it would use is older than 1.47.3 and linked against libfuse 3.
  - That `fuse2fs` keeps the old size of a file that is rewritten shorter. The image is damaged with no error, and `e2fsck` reports it clean.
- libexec's apptainer is not checked. It mounts with the `fuse2fs` of its own bundle, which is only released when that `fuse2fs` passes the smoke test.
- A `fuse2fs` whose version or libraries cannot be read never blocks a launch.

## Current

- `internal/build` records which binary produced a build, but must not decide that itself. Resolving separately could name a different binary than the one the build used.
- A build runs its container through `exec.Run`, which does not hand the `Bin` back.
  - So each resolver remembers the one it returned, and `Current` reports its implementation and version.
- `Current` errors when nothing has been resolved, rather than searching.
- The remembered value is read only for provenance. No launch takes its binary from it.

## Helpers

- libexec's apptainer finds `mksquashfs`, `unsquashfs`, `squashfuse_ll`, `fuse2fs`, `fuse-overlayfs` and `fakeroot-sysv` in its own tool directory.
- The host's apptainer looks in its own tool directory and `PATH`.

## Launch decisions

- Every launch passes `-q`, so Apptainer's INFO lines are hidden. Warnings and errors still print.
  - `--debug` leaves it off, to show what Apptainer does.
- Build operations unset `SINGULARITY_BIND` and `APPTAINER_BIND`, to avoid `%post` mount conflicts.
- Definition builds pass `--fix-perms`, so an OCI base's owner-unreadable files and no-write directories are packed with their content and removable at cleanup.
- Cancellation sends SIGTERM, then SIGKILL after `ExecOptions.StopGrace` (5s when unset).
- Environment values go as `APPTAINERENV_*`, not `--env`, which is parsed with CSV rules and also exposes values in `ps`.
