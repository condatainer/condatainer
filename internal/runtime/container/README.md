# internal/runtime/container

Turns a request for overlays into what an Apptainer launch needs.

## What Setup refuses

Checks run before anything is locked or mounted. Each describes a container that cannot be what the caller asked for.

- At most one writable `.img`. Only one can be the principal image, and only that one takes a write lock.
- At most one `.sif`. Its only valid use is the exec root, and two cannot both be it.
- No two images may claim one `/cnt/<name>` subtree.
- Overlays are disjoint subtrees, not stacked diffs. The later mount takes the whole subtree and the earlier one contributes nothing.
- Both would still reach `PATH` and the environment. That is a wrong container that looks like a working one, so it is an error.
- The case it catches is two builds of one name, such as a project's restored copy and a flat install.
- An `os` image and an image with no readable metadata record no prefix, so they are exempt.

## Root selection

- Every image a command wants is named the same way, through the overlay list.
- Root is pulled out of the plain requested order first. The `os`, `app` and `data` layering runs on what is left.
- A `.sif` wins unconditionally. At most one exists, and root is its only use.
- Otherwise the first `os` entry in the requested order wins.
- An `env` never supplies a root. An environment's identity presupposes a chosen root.
- The root is run as the exec root, not mounted with `--overlay`.
- When none is found, the caller supplies the fallback.
- `Setup` cannot build a missing configured default. `internal/build` would have to import this package's caller, which is an import cycle.
- A caller that wants one built asks `HasRequestedRoot` first, and only builds the default when it answers false.
- Building it unconditionally would be wasted work, and a needless failure when the request already names a root.
- A helper launch checks the root before submitting: the project's base pin, else the configured default. That matches what the job's own `exec` resolves on the node.

## Writable overlay

- The writable overlay is the `.img`, the diff layer on top of the read-only `.sqf` overlays.
- Only it gets `:rw`, and only when requested. Every other overlay is `:ro`.
- Writable adds no `--writable` to Apptainer.

## Overlay ordering

Root selection has already run, so ordering never decides which overlay becomes root.

- First the `os` overlays, in the order requested. (or `.sif` file)
- Then `app` and `data` overlays, in the order requested.
- Then the one `env` snapshot present, autoloaded or explicit.
- The writable `.img` is always last, so it is the top layer.

## Environment

- Each image's environment comes from its embedded `runtime.json` only.
  - The manifest is never opened here, so provenance can grow without slowing every mount.
  - There is no fallback to the manifest for an older image.
- A bad image never blocks a mount. One with no readable document mounts and contributes nothing.
- An image built for another architecture is mounted, contributes nothing and warns.
  - `runtime.json` is already in hand, so the check is a string comparison.
  - Without it a wrong-architecture image mounts cleanly and fails somewhere the cause is unrecognizable.
  - Only a recipe marked `#ARCH:noarch` is portable.
- One predicate decides which images reach `PATH` and activation.
- `PATH` gets no existence check. A missing entry is harmless, and checking would cost an archive read per image per invocation.

## Activation

- A conda package's `etc/conda/activate.d/*.sh` are its own activation scripts. They are sourced by default, as a real `conda activate` would.
- The command is wrapped in one `bash -c` to source them, since Apptainer's `--env` is a static list.
- Only activation is replayed. There is no deactivate.
  - A run mounts, executes once and exits, so there is nothing to switch away from or restore.
- `mm` is a bash function, a shortcut for `condatainer env`.
- `conda-meta/state.json` and `etc/conda/envvars/*.json` also set environment variables. They are left unread on purpose.
  - They are rare. Not all os overlays have the json parser.

## Binds

- With a conda environment mounted, the self-provisioned toolchain directory is bound at the same path as on the host.
  - In-container `mm` and `env` then find `micromamba`, without the base image carrying one.
- It is also bound when `nested_run` supplies apptainer, since apptainer needs its binary and libraries at the host path.
- The executable is bound at `/.cnt_bin`, not under `/usr/bin`. A bind there puts a mount boundary inside the directory `dpkg` unpacks into, and `dpkg` then refuses every package.

## Nested running

- `nested_run` decides whether a container can start containers of its own. The decision is made by the caller, not `Setup`.
- Providers, in order: the apptainer in `libexec/` (bound, never an overlay), else the newest installed `apptainer/<version>` overlay, else, only under `true`, one built on the spot.
- `auto` builds nothing and stays silent when there is no provider.

## GPU detection

- NVIDIA is detected by `/dev/nvidiactl`, AMD by `/dev/kfd`.
- A declared GPU always forces detection. `autoload_gpu` only decides use that declared none, such as a hand-typed `exec`.
- It defaults on, because the two failures differ.
  - Off, a user on a GPU node silently gets no GPU, and nothing points at `--gpu`.
  - On, the bad case is a loaded driver with an unusable GPU. `--nv` then fails loudly at container creation.

## Writable and snapshot environment overlay as a pair

- A freeze turns a writable `env.img` into an immutable `env` `.sqf`, and the `.img` is then thin. The two are one environment, loaded together.
  - Alone, the `.img` looks nearly empty and the snapshot is stale. Neither is the environment.
  - Everything that reads an environment reads the pair, so no consumer under-reports.
  - A freeze packs the pair, so the new snapshot holds both.
- The snapshot goes underneath before the `.img`'s first write.
  - Otherwise `fuse-overlayfs` marks a new directory opaque, and the mark hides the snapshot's copy for good.
- Ownership differs between the two, and that decides the layout.
  - Apptainer mounts a `.sqf` with `squashfuse_ll -o` overriding uid and gid. Whoever mounts it owns every file, so a snapshot is safe to share.
  - The `.img` is ext3 and keeps the uid and gid of whoever wrote it. Another user gets permission denied, and no mount option fixes that.
  - It also has one writer at a time.
- So the `.img` is per user and the snapshot is shared.
  - `env-<user>.img` is preferred over `env.img`.
  - The snapshot is `<stem>.sqf`, the `.img` name without `-<user>`. `env-alice.img` and `env.img` share `env.sqf`.
  - A per-user snapshot would buy nothing, since the override already removes the ownership problem.
- A file in the snapshot slot that is not an `env` snapshot pairs nothing. Guessing another file is the mistake to avoid, and a freeze must never replace an unrelated file.
- A snapshot with no `.img` is valid. A missing `.img` named directly is an error, never swapped for the `.sqf` beside it.
- The pair lookup lives here to avoid import cycles.
  - `container` depends on `internal/artifact/compare`, which depends on `key`, which depends on `conda`.
  - `internal/helper` imports `internal/project`.
