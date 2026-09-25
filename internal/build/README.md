# internal/build

Builds overlay images from Conda packages, recipes or Apptainer definitions.

## Two axes

- `BuildType` is how a target is built: Conda, Def or Script.
- `catalog.Type` is what the payload is. The two are independent, so a script build can be any type.
- Type only decides where the build works and how the archive is compressed.
- An `app` or `data` build writes to `$CNT_PREFIX`, `/cnt/<name>`. That prefix is recorded in the image, so anything baked into the payload stays valid at run time.
- An `os` build has no prefix. Its payload is the root filesystem itself.

## Dependencies

- `#DEP:` is a build dependency and nothing else. It names what must be mounted while the recipe runs.
- It is not recorded in the image and not re-expanded at mount time. There is no runtime dependency tree.
- An `app` is therefore self-contained. `data` is the type that normally has dependencies, because producing an index needs the tool that produces it.
- `#DEP:samtools/1.22.1>=1.10` accepts any installed version from 1.10 up to 1.22.1. Nothing above the preferred version is accepted.
- A satisfying installed version is mounted. With none installed, the preferred version is built.
- The dependency edge in the artifact's keys is read from the image that was mounted. Reading the preferred version instead would record a tool that never ran.

## Locked rebuilds

- A locked rebuild builds one artifact from a project lock's vendored records instead of the catalog.
- It is defined by what it refuses: no catalog lookup, no fuzzy version choice, no prebuilt pull by name, no flat install.
- Dependencies are absolute paths, one per manifest edge. Everything downstream reads a dependency's own manifest, so an edge is recorded the same way either way.
- The output belongs to the caller. An occupied output is refused, since restore builds into a temporary sibling and renames.
- A template runs expanded and embeds the template. The placeholders come from the manifest, the only record of which variant this is.
- A definition keeps its recorded upstream digest. Re-resolving would follow a tag that has since moved.
- A Conda rebuild replays `explicit.txt`, whose bytes are the recorded identity. `environment.yml` would be a fresh solve against whatever the channels serve today.
- The root is supplied by the caller, from the lock's base pin, and is never resolved. A `.def` rebuild bootstraps its own root and never reads it.
- Nothing checks the result. The rebuild derives its own keys, and the caller compares them with the lock.
- A disagreement therefore surfaces as a key mismatch, not as a plausible artifact under the right name.

## The build lock

- Each target has a producer lock at the image path plus `.lock`.
- The lock lives in `internal/image/producer`, so a build and a registry pull serialize on the same path.
- It is a different lock from the inode lock held while an image is mounted. The producer lock coordinates creation, and the inode lock protects readers at replacement.
- A stale lock is cleared only when its owner is provably gone: a dead local PID on this node, or a scheduler job that is no longer alive.
- Anything that cannot be verified is an error, not a guess: a lock from another node, or no scheduler to ask.

## Sources

- The tool fetches a `#SOURCE:` file, not the recipe, because the tool can only hash what it downloaded.
- The file is bound read-only, so a recipe cannot alter a file whose digest is already recorded.
- The digests enter the identity.
- The fetch sits after the prebuilt check and before the installed check. A published artifact is decided on equivalence, which no source enters, so nothing is downloaded to answer a question it could have answered.
- The fetch must stay below the lock step. On a compute node the lock adopts the scheduler's lock and moves the workspace root, and the source directory is resolved from it afterwards.
- `ask:` prompts join the `#INPUT:` prompts in one list, so a submitted build needs no terminal.
- A short read fails and deletes the partial file. The digest of a truncated file is a valid digest, and would fix an identity for bytes nobody wants.
- An answered link carries an auth token. A log line names the host only, and the transport error has the request URL stripped.
- Compression is off, so the file is the bytes the server sent.
- A transfer that receives nothing for five minutes fails instead of hanging the build. A slow, steady one never does.
- Sources are transient and go with the workspace.

## Staging and packing

- Metadata is staged in `.cnt/`, beside the payload and not inside it.
- `mksquashfs` makes one archive root per source, so two sources give `/cnt/<name>/…` and `/.cnt/*.json` at one level, without the payload containing a directory the recipe did not create.
- There is no option to rename a source, so the staged directory must already be called `.cnt`.
- The recipe is staged as it was fetched. A template keeps its `{placeholder}` tokens, and the expansion is never embedded.
- Every variant of one template therefore shares a recipe digest. `manifest.source.placeholders` tells them apart.
- Keys are derived immediately before staging, in every backend. That is the last point at which everything a key depends on is known.
- A script build derives them after the recipe has run, so every dependency it needed is installed and its exact identity can enter the key.
- The payload key is computed right before `mksquashfs` runs. It is the last moment the payload is final, and it is the directory `mksquashfs` reads.
- A build that cannot key its payload does not pack. Only script builds are keyed.
- The payload key is not an input to identity or equivalence.
- Validation runs at staging. Earlier there is nothing to validate, and later the image already exists.
- Every `.def` build is keyed the same way, `os` included. Its keys cover the definition plus the upstream image it bootstrapped from.

## The upstream digest

- A definition's `Bootstrap:` and `From:` are read from its header. A `From:` inside `%post` is shell text, not a directive.
- The reference is resolved to a digest before the build. The digest enters the identity, and `From:` is rewritten to name it in the definition handed to Apptainer.
- Without the pin, an upstream retagged mid-build would leave the identity describing bytes the image does not contain.
- Only the transient copy is rewritten. `/.cnt/recipe` keeps the definition byte for byte.
- Resolution never fails a build. An unreachable registry records `unrecorded` and warns, because a login node behind a proxy must still be able to build.
- A bootstrap with no upstream at all (`scratch`, `localimage`, `debootstrap`) records nothing, so the two cases stay distinguishable.
- A `scheme://` build synthesizes a definition, which becomes the recipe. Otherwise a `docker://ubuntu:24.04` image would carry no keys.
- Its directives are byte for byte what Apptainer writes to `/.singularity.d/Singularity`. An image built here and a foreign one built from the same URI then share a recipe digest, and share an identity whenever they resolved the same upstream.

## Prepared output

- Every build writes to a prepared path beside the target, tagged with the lock owner, and renames it over the target.
- Beside the target so the rename cannot cross filesystems.
- Every build, not just an update. Writing straight to the installed path would leave a truncated image indistinguishable from a finished one.
- The installed image is never removed first. A rename over an existing file is atomic, so a reader sees the old image or the new one, never a gap.
- Removing first would also destroy the installed copy if the rename then failed.
- The name is derived from the lock owner, not recorded. That is how stale-lock cleanup finds the orphaned output of a killed build.

## The container root

- Script and Conda builds run their install inside a container, so each has an implicit edge to the container root. It is separate from anything `#DEP:` declares.
- The graph resolves it once before any node runs, and builds the configured default root first if none is installed.
- The root is an ordinary `.def` recipe of type `os`, built through the normal build path. It has no dedicated path.
- The install runs in a container because `$CNT_PREFIX` is baked into what gets installed. Shebangs, activation scripts and some RPATHs are not relocatable.
- The container root supplies that absolute path. It is not there for tool availability.
- Packing never runs in a container. `mksquashfs` runs on the host and reads a directory exactly as it would through a bind mount.
- Conda installs run through `internal/libexec`'s toolchain, not whatever the root happens to carry.
- A `.def` build has no base of its own. A definition bootstraps its own root.
- Any `.def` build may end up chosen as someone's root, so each checks its sandbox for `/bin/bash`. Every later `execpkg.Run` launches that exact path.
- The check asks the container, not the sandbox directory. `command -v` answers with the PATH `/bin/bash` will run under, where a file check could guess wrong.
- The configured default root is found by searching every image path, so a root supplied by a shared install is not rebuilt into the user's directory.

## Importing a foreign root

- `-f` also accepts a `.sif` or an Apptainer sandbox directory someone already built. No Apptainer runs.
- It is a separate constructor, not a third case of the definition build. A definition resolves its upstream and then asks Apptainer to fetch it. An import has nothing left to fetch.
- Identity comes from the root's own record, never a fresh resolve. The digest is the `org.opencontainers.image.base.digest` label when present.
- The label is optional, since older builds carry only legacy labels. Its absence records `unrecorded` and does not fail the import.
- Only `docker://`, `oras://` and `library://` roots import. Everything else is refused before any key is derived.
- The rest cannot be rebuilt from a fresh machine. `shub` is dead. `localimage` names a path on the original builder's machine. `scratch` has no upstream a `.sif` preserves.
- `yum`, `zypper` and `debootstrap` use a mirror-plus-version shape that the identity model's single `From` string cannot represent.
- A live build from a dead scheme fails at Apptainer's own fetch. An import reads a root that already built, so it has no such safety net and must refuse explicitly.
- Extraction never copies the tree out first. A sandbox packs directly. A `.sif` mounts its SquashFS partition read-only, in an unprivileged namespace.
- Everything is repacked with `mksquashfs -all-root -no-xattrs`. A real service image can carry non-root ownership that unfreeze's single-identity mapping cannot handle later.
- The architecture is read from the SIF header, which is always present. A label is self-reported.
- A mismatch with the host refuses the import, rather than packing an artifact that reports itself as native and never runs.

## Graph execution

- Solving walks the graph from the index alone. It resolves transitive dependencies, detects cycles and orders them, without fetching a recipe.
- Prebuilts are decided at plan time, so the plan can say what will happen and a pull is never queued behind a scheduler job.
- Each node derives its equivalence from its recipe, placeholders and what its dependencies contribute. An installed dependency contributes its own keys. One still to be acquired contributes the key derived for it earlier in the pass, so no dependency has to be installed to decide.
- A dependency that is neither installed nor planned, such as a path or an unsatisfiable constraint, leaves the node undecided. It looks when it builds.
- A pull opens none of its build dependencies. The nodes only pulled nodes need are dropped from the plan.
- A pull that falls through to a build acquires them then. That is why a planned pull is tried before the toolchain and the dependencies.
- The base is resolved for every script and Conda build before any node runs, because a scheduler job cannot build one on the node.
- A pull is always local.
- A build goes to the scheduler when its recipe carries directives, or when it is `data` and `build.always_submit_data` is set.
- A job re-runs `create` from the name. Only a build that command can reproduce is submitted: a catalog name, or an external shell script.
- A Conda build from packages or a file, a definition and an import run locally.
- The flags that change what is built or where it lands travel with the job. Submission flags do not: the job is the submission.
- `--no-prebuilt` is set on a node that planning decided to build, so the node need not look again.

## Workspace

- Each producer owns one directory under the tmp root, named for the build and the lock owner. Cleanup removes only the current owner's directory.
- The recipe is materialized in a process-private directory before the target lock, because resolution and scheduler planning need to read it. Taking the lock re-sites that directory to the lock owner.
- The definition flag is passed, not inferred. Every build produces a `.sqf`, so nothing in the paths would distinguish a definition.
- Every payload is written to a host directory. There is no scratch image.
- A build that must keep small files off a quota-limited filesystem points `$CNT_TMPDIR` at local scratch.

The tmp root depends on the build.

- The fast root is the scheduler's scratch, then `$TMPDIR`, then `/tmp`, always with `cnt-$USER`.
- The stable root is the first writable data-directory `tmp`. It falls back to the fast root when none is writable.
- Conda builds and `app` scripts use the fast root. `data` scripts use the stable root.
- An external `data` build keeps its intermediates beside the target. The user picked that location, and a multi-GB payload is the thing least able to survive a scratch quota.
- A definition build must use the fast root. Apptainer builds under `--fakeroot`, which NFS, Lustre, GPFS and PanFS do not support, and a data directory on an HPC system is often one of those.
- An external `.def` cannot follow its target for the same reason.
- The constraint lands on the workspace root, not on `APPTAINER_TMPDIR`. Apptainer assembles a sandbox next to its destination.
- If that filesystem cannot support `--fakeroot`, Apptainer warns, builds in a temporary directory and copies. That degraded path is what choosing the workspace root well avoids.
- `APPTAINER_TMPDIR` is still set to the fast root. Unset, it inherits `TMPDIR`, which on a scheduler is often the network scratch this design avoids.
- `$CNT_TMPDIR` moves the fast root and nothing else. Exporting it to speed up a Conda build must not move the next data build onto scratch the job wipes.

## Failures

- A missing self-provisioned tool fails in Go, before any work starts.
- Micromamba has no other legitimate source, so there is nothing to fall back to and no reason to spend a container launch finding out.
- `mksquashfs` is resolved the same way, before any script is rendered.
