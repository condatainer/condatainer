# internal/runtime/apptainer

## Which binary

- Two constraints decide it.
  - Fakeroot needs a setuid starter, which only the host's apptainer or a module's has. libexec's apptainer is deliberately non-setuid.
  - Every artifact is zstd, which only apptainer `>= 1.4` can mount. Singularity cannot.
- A run therefore wants one thing: an apptainer `>= 1.4`. libexec's supplies it where the host's is older, so a run behaves the same on every host.
- The caller resolves the binary it needs, and the launch carries it in `Bin`. `exec.Prepare` picks once fakeroot is final.
- `Normal` is for an ordinary launch.
  - libexec's apptainer when installed, so installing one is how a user chooses it over the host's.
  - Otherwise the host's, refused below the floor. The refusal names the way out, since a later failure inside Apptainer's mount step would not.
  - Inside a container that advice cannot be followed, so the message points at `nested_run` or an apptainer overlay.
- `Fakeroot` is the host's only, and checked against the floor.
- `ForBuild` is for a `.def` build's own `apptainer build --fakeroot`.
  - It is the host's with no floor. The output is a sandbox, and `#DEP:` is data-only, so no zstd artifact is mounted during the build.
  - Packing the sandbox runs `mksquashfs` on the host, never in a container.
- `Fakeroot` and `ForBuild` refuse at once inside a container with `ErrNeedsHost`. No starter there can escalate.

## Current

- `internal/build` records which binary produced a build, but must not decide that itself. Resolving separately could name a different binary than the one the build used.
- A build runs its container through `exec.Run`, which does not hand the `Bin` back.
  - So each resolver remembers the one it returned, and `Current` reports its implementation and version.
- `Current` errors when nothing has been resolved, rather than searching.
- The remembered value is read only for provenance. No launch takes its binary from it.

## squashfs-tools in PATH

- Apptainer needs `unsquashfs` and `mksquashfs` in `PATH` for some of its own operations, such as extracting a `.sqf` to build a sandbox.
- That is Apptainer's own subprocess lookup. It is separate from the environment a launched container's `PATH` is set from.
- `runApptainerWithOutput` adds libexec's `bin/` to `PATH` whenever the binary is libexec's own, so it finds the squashfs-tools provisioned beside it.
  - Without it: `exec: "unsquashfs": executable file not found in $PATH`.

## Launch decisions

- Build operations unset `SINGULARITY_BIND` and `APPTAINER_BIND`, to avoid `%post` mount conflicts.
- Definition builds pass `--fix-perms`, so an OCI base's owner-unreadable files and no-write directories are packed with their content and removable at cleanup.
- Cancellation sends SIGTERM, then SIGKILL after `ExecOptions.StopGrace` (5s when unset).
- Environment values go as `APPTAINERENV_*`, not `--env`, which is parsed with CSV rules and also exposes values in `ps`.
