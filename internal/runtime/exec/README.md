# internal/runtime/exec

Ephemeral container execution: the container setup pipeline plus Apptainer.

## Root

- The root is `Setup`'s `Root` when the requested overlays name one, else the caller's `BaseImage`, else the configured default.
- The root is required. There is no overlay-only execution, so a container with no root cannot start.
  - `Prepare` fails instead of letting Apptainer report a missing file.
- It must exist. That is the only check. No manifest type is read before accepting something as root.
- Building a missing default root is the caller's job. This package runs images. `internal/build` knows how to make them.

## Apptainer binary

- There is no `ApptainerBin` field. The caller cannot choose the binary.
- It is decided from the final `Fakeroot` value, which is why it is resolved after the root and fakeroot are settled.
- Fakeroot is enabled automatically for a writable `.img` overlay.

## Locks

- Shared locks are held on every `.sqf` overlay and the base image while the container runs.
  - That stops a concurrent `remove` or `build --update` from deleting a file in use.
- `.img` overlays are skipped. Apptainer locks them itself, and ours would conflict.

## Proxy and activation

- An active SOCKS5 proxy, found by `proxy.FindActiveProxy`, is injected as `http_proxy`, `https_proxy` and `all_proxy` (and uppercase), so tools inside use the tunnel.
- An overlay's own `etc/conda/activate.d` scripts are sourced by rewriting the command as `bash -c <activation + exec "$@">`.
  - A static `--env` list cannot express it. It is shell script, not values.

## Conda overlay creation

- `CreateCondaOverlay` installs into a scratch ext3 image and moves it to the destination only after the install succeeds.
- The scratch path is disconnected from the destination's paired snapshot, so `Setup`'s autoload beside the given path cannot find it.
  - `CreateCondaOverlay` looks the snapshot up against the final path and mounts it with the scratch image.
  - Without that, an install would repeat everything the snapshot already has, instead of writing only the difference.
- `InstallPackages` and `RemovePackages` run against the real path, so the ordinary autoload already finds the pairing.
