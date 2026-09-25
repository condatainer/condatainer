# internal/toolpath

The one place that decides which binary runs for an external host tool. Nothing else resolves a bare tool name.

## Why a separate package

- Three concerns were tangled, and only one is generic.
  - Provisioning the self-provisioned toolchain (`internal/libexec`).
  - Finding a runnable path for a name.
  - Shaping errors from image tools (`internal/image/tool`).
- `internal/conda` needs the lookup and has no use for the image error shape.
- `internal/image` already imports `internal/image/squashfs`. Resolving through anything that leads back to `internal/image` would cycle.
- A separate package is importable from `conda` and from `image` and its children alike.

## Why the preference lives here

- `libexec` answers "do I have this, and where". It has no opinion about `PATH`, the FHS directories or which source wins.
- This package decides.
  - The provisioned copy comes first. It is required, so it wins even over a same-named binary on `PATH`.
  - Then every `PATH` entry.
  - Then the tools bundled with the host apptainer, read from `apptainer buildcfg` once per process.
  - Then the FHS directories.
- A host `mksquashfs` or `unsquashfs` below squashfs-tools 4.4 is skipped, with the reason kept for the error. A good copy later in the order can still win.
- Nothing is special-cased. A name libexec never provisions, such as e2fsprogs, is simply not found there and falls through.

## Cache

- Running host binaries (`apptainer buildcfg`, a tool's version flag) is remembered in a per-user `toolpath.json` in the personal cache directory.
- Each entry records the size and mtime of the binary and is ignored when they differ.
  - An unloaded module, an upgrade, or another node's different binary just misses.
- There is no on-disk cache inside a container, where the same path can name a different binary than on the host.
- Config is not used. A config file can be a group layer read on hosts with different modules.
- The cache is never needed for correctness. Deleting it is harmless.

## Host-side and container-bound

- `Resolve` and `Command` are for a host-side call with no bind mount to build. Examples are freeze's own packing and reading `conda-meta` out of an image.
- A container-bound call needs the tool's containing directory bound in too, so the absolute path resolves inside the container.
  - It bypasses this package and uses `libexec.ApptainerPath`, `MksquashfsPath` or `MicromambaPath`.
- `Command` is `Resolve`, `exec.CommandContext` and a debug log. It has no opinion about stdout, stderr or error shape.
  - Callers wrap the result in their own error type, since they need different information.

## Not routed here

- Apptainer is a policy choice between two named candidates, gated by fakeroot and a zstd floor. `Resolve` has nothing to offer it.
- Scheduler tools and the proxy tools (`ssh`, `loginctl`) keep their own resolution.
  - libexec never provisions them, so routing them here would only reach the `PATH` and FHS fallback.
