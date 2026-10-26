# internal/build

Builds overlay images from Conda packages, recipes or Apptainer definitions.

## Two axes

- `BuildType` is how a target is built: Conda, Def or Script.
- `artifact.Type` is what the payload is. The two are independent, so a script build can be any type.
- An `app` or `data` build writes to `$CNT_BUILD_PREFIX`, `/cnt/<name>`. The prefix is recorded in the image, so anything baked into the payload stays valid at run time.
- An `os` build has no prefix. Its payload is the root filesystem.

## Dependencies

- `#DEP:` is a build dependency and nothing else. It names what is mounted while the recipe runs, and there is no runtime dependency tree.
- Only `data` may declare one. An `app` is self-contained, and an `os` is by definition.
- A version range admits any installed version up to the preferred one. With none installed, the preferred version is built.
  - `>=` is for reuse, not widening. `samtools/1.23.1>=1.10` admits `[1.10, 1.23.1]`.
  - Handing micromamba a range would let two machines resolve one recipe to different versions.
  - With no preferred version, the newest installed version in range wins, so a bare `#DEP:` does not rebuild when upstream moves.
- The dependency edge in the keys is read from the image that was mounted, so it never records a tool that did not run.
- A build's `#DEP:` is a `name/version`, never a path. A running script's may be either.
  - A running script mounts what it names and records nothing.
  - A build's declaration becomes an edge in an artifact that must mean the same thing on another machine, and a path is neither resolvable there nor a key anything can regenerate.
- `validateDeps` holds both rules. `validateRecipe` and `FromExternalSource` call it, never `run` or `check`.
- A `.sif` is a root-only, literal-path reference. It never stacks and carries no build identity.

## `#TARGET:` names the artifact

- Without a `#PH:`, `#TARGET:` is not a template. It is how an external build gets a name.
- Without a name, `key.Role` never matches a dependency and silently downgrades every `#DEP:` to build history. So an external script that declares a dependency must have one.
- The name and the file path are separate. `#TARGET:` fixes `/cnt/<name>` and the role. `-p` fixes where the `.sqf` lands.
- `create --name` beside `-p` wins over `#TARGET:`.
- A path-addressed artifact's filename carries no naming claim. A flat or store scan still requires the filename to encode the name, since there it is the address.

## Locked rebuilds

- A locked rebuild builds one artifact from a project lock's vendored records instead of the catalog.
- It is defined by what it refuses: no catalog lookup, no fuzzy version choice, no prebuilt pull by name, no flat install.
- Dependencies are absolute paths, one per manifest edge.
- A template runs expanded and embeds the template. The placeholders come from the manifest.
- A definition keeps its recorded upstream digest, and a Conda rebuild replays `explicit.txt`. Re-resolving would follow a moved tag or a fresh solve.
- The caller supplies the root from the lock's base pin. A `.def` rebuild bootstraps its own.
- Nothing checks the result. The rebuild derives its own keys, and the caller compares them with the lock.

## The build lock

- Each target has a producer lock at the image path plus `.lock`, in `internal/image/producer`, so a build and a registry pull serialize on one path.
- It coordinates creation. The inode lock coordinates readers at replacement.
- A stale lock is cleared only when its owner is gone, under an exclusive lock.
  - A scheduler job is asked of the scheduler, and one that cannot be asked is an error, not a guess.
  - Anything else is alive while its holder keeps an `fcntl` lock. The kernel drops it however the holder dies.
- A lock records the full hostname, so two hosts sharing a short name are not one node.

## Sources

- The tool fetches a `#SOURCE:` file, not the recipe, because it can only hash what it downloaded. The digests enter the identity.
- The file is bound read-only, so a recipe cannot alter a file whose digest is recorded.
- The fetch sits after the prebuilt check and before the installed check, so nothing is downloaded to answer a question equivalence could answer.
- It stays below the lock step. On a compute node the lock moves the workspace root, and the source directory is resolved from it.
- `ask:` prompts join the `#INPUT:` prompts in one list, so a submitted build needs no terminal.
- A short read fails and deletes the partial file, since the digest of a truncated file is still a valid digest.
- Logs name the host only. The transport error has the request URL stripped.
- A transfer that receives nothing for five minutes fails.
- Sources are transient and go with the workspace.

## Staging and packing

- Metadata is staged in `.cnt/` beside the payload. `mksquashfs` makes one archive root per source and cannot rename one, so the staged directory is already called `.cnt`.
- The recipe is staged as fetched. A template keeps its `{placeholder}` tokens, so every variant shares a recipe digest and `manifest.source.placeholders` tells them apart.
- Keys are derived immediately before staging, in every backend, when everything they depend on is known.
- A script build derives them after the recipe has run. The payload key is computed right before `mksquashfs`, and a build that cannot key its payload does not pack. The payload key is not an input to identity or equivalence.
- `build.created` is stamped at staging, or taken from `SOURCE_DATE_EPOCH`. It never enters a key.
- Every `.def` build is keyed, `os` included. Its keys cover the definition and the upstream image it bootstrapped from.

## The upstream digest

- A definition's `Bootstrap:` and `From:` come from its header. A `From:` inside `%post` is shell text.
- The reference is resolved to a digest before the build and enters the identity. `From:` is rewritten to name it, in the transient copy only. `/.cnt/recipe` keeps the definition byte for byte.
- Without the pin, a retagged upstream would leave the identity describing bytes the image does not contain.
- Resolution never fails a build. An unreachable registry records `unrecorded` and warns, so a login node behind a proxy can still build.
- A bootstrap with no upstream (`scratch`, `localimage`, `debootstrap`) records nothing, which stays distinguishable from `unrecorded`.
- A `scheme://` build synthesizes a definition, which becomes the recipe, so an image built here and a foreign one from the same URI share a recipe digest.

## Prepared output

- Every build writes to a prepared path beside the target, tagged with the lock owner, and renames it over the target.
- Beside the target so the rename cannot cross filesystems. Every build, so a truncated image is never indistinguishable from a finished one.
- The installed image is never removed first. A rename is atomic, so a reader sees the old image or the new one, and a failed rename leaves the old one.
- The name derives from the lock owner, so stale-lock cleanup finds the orphan of a killed build.

## The container root

- Script and Conda builds install inside a container, so each has an implicit edge to the root, separate from `#DEP:`.
- The graph resolves it once before any node runs, and builds the configured default first if none is installed. The root is an ordinary `.def` recipe of type `os`.
- The install runs in a container because `$CNT_BUILD_PREFIX` is baked into the result. Shebangs, activation scripts and some RPATHs are not relocatable. The root supplies the path, not the tools.
- Packing runs on the host. Conda runs through the `internal/libexec` toolchain, not what the root carries.
- A `.def` build bootstraps its own root. Any `.def` may become someone's root, so each checks its sandbox for `/bin/bash`, asked of the container because `command -v` answers with the PATH it will run under.
- The default root is found across every image path, so a shared install's root is not rebuilt into the user's directory.

## Importing a foreign root

- `-f` also accepts a `.sif` or an Apptainer sandbox. It is a separate constructor from a definition build, since there is nothing left to fetch.
- Identity comes from the root's own record, never a fresh resolve. A missing `org.opencontainers.image.base.digest` label records `unrecorded` and does not fail.
- Only `docker://`, `oras://` and `library://` roots import. The rest cannot be rebuilt on a fresh machine, or have a mirror-plus-version shape the identity model cannot represent. Without Apptainer's own fetch as a safety net, the import refuses explicitly.
- Everything is repacked with `mksquashfs -all-root -no-xattrs`, because non-root ownership breaks unfreeze's single-identity mapping.
- The architecture is read from the SIF header. A mismatch with the host refuses the import.

## Graph execution

- Solving walks the graph from the index alone: transitive dependencies, cycles and order, without fetching a recipe.
- A name no source provides is a leaf, built as Conda. Its own package tree is micromamba's, so the graph never holds it.
- Prebuilts are decided at plan time, so a pull is never queued behind a scheduler job and opens none of its build dependencies.
- Each node derives its equivalence from its recipe, placeholders and what its dependencies contribute. A dependency still to be acquired contributes the key derived for it earlier, so none has to be installed to decide.
- A dependency that is neither installed nor planned leaves the node undecided. It looks when it builds.
- The base is resolved for every script and Conda build before any node runs, because a scheduler job cannot build one on the node.
- A pull is always local.
- A build goes to the scheduler when its recipe carries directives, or when it is `data` and `build.always_submit_data` is set.
- A job re-runs `create` from the name, so only a build that command can reproduce is submitted: a catalog name or an external shell script. Conda from packages or a file, a definition and an import run locally.
- Flags that change what is built or where it lands travel with the job. Submission flags do not.

## Workspace

- Each producer owns one directory under the tmp root, named for the build and the lock owner. Cleanup removes only the current owner's.
- Every payload is written to a host directory. There is no scratch image.
- A build that must keep small files off a quota-limited filesystem points `$CNT_TMPDIR` at local scratch.
- The fast root is the scheduler's scratch, then `$TMPDIR`, then `/tmp`, always under `cnt-$USER`. The stable root is the first writable data-directory `tmp`, or the fast root when none is writable.
- Conda builds and `app` scripts use the fast root. `data` scripts use the stable root.
- An external `data` build keeps its intermediates beside the target, since a multi-GB payload is least able to survive a scratch quota.
- A definition build must use the fast root, because Apptainer builds under `--fakeroot`, which NFS, Lustre, GPFS and PanFS do not support. An external `.def` cannot follow its target for the same reason.
- `APPTAINER_TMPDIR` is set to the fast root. Unset, it inherits the scheduler's often-network `TMPDIR`.
- `$CNT_TMPDIR` moves the fast root and nothing else, so speeding up a Conda build never moves the next data build onto scratch the job wipes.

## Failures

- A missing self-provisioned tool (micromamba, `mksquashfs`) fails in Go before any work starts. There is nothing to fall back to, and no reason to spend a container launch finding out.
