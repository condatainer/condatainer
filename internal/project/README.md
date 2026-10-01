# internal/project

`cnt-lock/` is the tracked record of which exact artifact satisfies each dependency a project declares.

## What the lock holds

- Pins, plus the vendored manifests and rebuild sources. Everything git should carry.
- Nothing machine-local: no payload, no absolute path, no hostname, no restore result.
- A checkout is a complete rebuild specification on a machine that has never run CondaTainer.
- Nothing derivable is serialized.
- Requests come from rescanning the scripts. Identity, type, platform, sources and edges come from the vendored manifests. The closure comes from following edges.
- A stale copy therefore cannot disagree with the truth.
- Output is deterministic, so a lock shows up in a diff only when it changed.
- Unknown fields are rejected. A lock from a newer build may mean something this one would drop on the next write.
- One entry directory is one artifact's records: `manifest.json` plus the sources it names.
- It is the same directory an image carries at `/.cnt/provenance/`, read by the same code. Two readers could drift into a lock that verifies against an image that does not.
- `.helper-history/` is the one gitignored exception. It is machine-local recency, and a checkout restamps mtimes.

## Remotes

- A remote is a repository coordinate plus a platform manifest digest. Never a mutable tag.
- Remotes are keyed by artifact, at the top of `lock.json`. A closure-only dependency can have one too.
- A remote never lives inside an entry directory. An entry's bytes are the key preimage, and a registry location is mutable information. It must change without the artifact changing.
- Remotes are written by machines, never typed.
- One is recorded only when something confirmed the artifact is there: a collection's registry advertises this exact identity, or `project registry push` just put it there.
- A digest nobody verified is a lock entry that fails on someone else's machine.
- A remote cannot live in the artifact. It is a digest over the pushed content, so embedding it would need the digest before the bytes exist.
- Order is retry priority. A pin records its upstream remote before any push, so the free location is tried first.

## Manual pins

- `Pins` is a projection of the scan. Reconcile deletes every key the scan does not produce, so the map can be rebuilt from nothing.
- A manual pin opts out of that sweep and nothing else.
- Some artifacts are named by no `#DEP:`:
  - A helper's `#REQUIRED_OVERLAYS:`. The scanner never reads it, and a helper never pins. Those overlays are the user's to pin later, if they want reproducibility.
  - A frozen environment. It is what analysis runs in, not something a script consumes.
- The manual flag is derived once, at pin time, then recorded. Deriving it on every run would turn the sweep into a no-op.
- A scan that cannot run answers "declared". The pin stays sweepable, so a later `project lock` can still correct it.
- Re-pinning never clears the flag. What changed is which artifact answers, not why the pin is there.
- `project unpin` is the only way out. A scan-produced pin is refused there, since the next Reconcile would pin it again.
- No scan shows a manual pin, so `project list` reads the lock and marks them.
- Overlays a helper used but the project does not pin are reported apart from `#DEP:` findings. They are never written as pins.
- No recorded helper usage is reported as a fact. It is never a suggestion to unpin: a helper that has not run since recording began looks the same as one that no longer needs the pin.

## The project's root

- `base:` is a reserved pin key. It holds the artifact restore treats as the container root.
- Without it, a project would mean whichever `default_distro` the restoring machine has.
- It is an ordinary pin at a fixed key. No manifest type says "this is a root", because `os` covers both.
- It is written on every `project lock`. There is no closure to walk to decide, and recording one costs nothing.
- Its one source is the configured default distro. `project set-distro` overrides it as manual, and `--auto` clears the override.
- Reconcile never sweeps it, and the scan never produces it.
- Restore runs it first. Nothing in the manifest graph points at it, since `os` carries no `#DEP:`, and every rebuild needs its path.
- `--only` cannot drop it. A job rebuilding one artifact must resolve its root from the lock, not from the compute node's configuration.
- A `.def` rebuild ignores it. It bootstraps its own root.
- Inside a project, `exec`, `e` and `run` resolve the root from the lock. An unresolved root refuses, naming `project restore`. It never falls back to the machine's `default_distro`.
- Bare names (`avail`, `list`, `remove`, `info`, completion, `create`) use the project's distro too, so `build-essential` means one artifact whoever types it.
- The distro is read from the base pin's manifest name, so a fresh clone answers before restoring anything.
- Those lookups fall back to the configured distro instead of refusing. They serve display and completion.

## Where a project publishes

- The destination and its `audience` are tracked. Every collaborator pushes to the same package.
- The coordinate grammar is duplicated here, not imported from `registry`. That keeps `Verify` checkout-local: no network, no credentials, no registry.
- The source repository is derived once from the git origin and recorded. Two collaborators with different remote spellings would otherwise publish different source annotations.

## Publishing

- Push gates on the lock checks of `project validate`, not on `Verify` alone. Verify cannot see a declaration nothing can pin, so a lock can be valid and still unrestorable elsewhere.
- A dry run reports those problems and still plans.
- Push never builds. An artifact missing at its locked identity is a refusal naming the restore.
- Publishing something the checkout did not describe would put bytes in a registry that no lock vouches for.
- Planning has two halves. `Build` works from the checkout alone. `Refine` asks the network whether a collection or the destination already has each artifact.
- Refine only removes work, so an unreachable registry leaves the plan as computed.
- A frozen environment inverts the usual fallback.
- Every other artifact can be rebuilt from its vendored recipe, so a registry is an optimisation. A snapshot vendors nothing, so a registry copy is the only source.
- It is refused at plan time as unavailable, not planned as a build that must fail.
- `--no-prebuilt` is ignored for it. Building is impossible, so no preference is left to express.
- Recording is one lock transaction per artifact. An interrupted push keeps everything that landed, and a rerun skips it.

## Scanning

- A declaration counts wherever it is written. The scanner and `run` share one tokenizer, so they cannot disagree about a script.
- Only `.sh` and `.bash` files are read. `run` executes project scripts with `/bin/bash`.
- Symlinked directories and scripts are never followed. A lock describes the checkout.
- `cnt-lock/` and every dot-directory are skipped. They hold tool state (`.venv`, `.snakemake`) whose scripts are not this project's.
- A build recipe is skipped. Its `#DEP:` are build dependencies already recorded in that artifact's provenance.
- A recipe is identified by `$CNT_PREFIX`. That is a property of the file itself.
- A folder convention or a sibling `.sqf` would make a script's meaning depend on something outside it.
- An analysis script must name an exact version. A result should say which version produced it.
- That also keeps one module from having two keys.
- A declaration is one of four kinds:
  - A name pins. It resolves by identity.
  - A project-relative `.sqf` pins. Restore owns that path.
  - A writable `.img` does not pin. It has no identity.
  - An external `.sqf` does not pin. Restore does not own that path.
- Both unpinnable kinds fail for one reason: a pin records where an artifact will be, and neither has such a place.
- An unpinnable declaration is always a finding. `project validate` fails, and `project lock` warns and still publishes the rest.
- Nothing silences one. A marker would make the hole a formality, and the hidden artifact is usually the environment itself.
- The remedy differs by kind. Freeze a writable `.img`. Copy an external `.sqf` under the project.
- One finding is reported per dependency, decided after every script is read.

## The project root is the one anchor

- A relative path in a declaration means relative to the project root, never the working directory.
- Mounts are resolved to absolute paths, so where a job runs does not matter to them.
- A job runs where it was submitted from, unless the script names a directory. A project can hold steps in subdirectories, each run from its own.
- A declared path may also resolve beside its declaring script, as a fallback address. The stored key is always root-relative.
- Only pinning checks the filesystem for candidates.
- Reconcile and Resolve check pin membership only, so a fresh checkout resolves before anything is restored.
- A `name:` request also matches an existing pin whose version it admits, newest first. Two scripts naming one dependency at different precision then agree.

## Where a restored artifact lands

- What asked for an artifact decides where it goes. Depth in the graph decides nothing.
- A named pin is addressed by identity. Any readable images root satisfies it, and a new copy lands where the store's rule puts it.
- A `path:` pin is a project output. It must exist at exactly the declared path.
- A copy in an images root is not a substitute. Adopting one would report success while the path stays empty and the script fails at mount time.
- A build dependency is a closure node no pin names. It is not installed.
- It is built in a restore-scoped directory and removed afterward, even on failure or cancellation.
- A dependency is needed to rebuild an artifact, never to use it. One already installed is adopted in place.
- `--keep-build-deps` installs new ones by the named rule instead.
- When a pin and a dependency share a name at different identities, the pin keeps the bare name. The flat name answers `exec -o`, `list` and other checkouts, so it belongs to what someone asked for.
- The restore directory is under the stable writable tmp root, not `CNT_TMPDIR`. Several conda environments exceed node-local scratch, and a job sweep must not remove it mid-restore.
- A destination that already holds something not matching the lock is recorded as a replacement in the plan.
- Replacing is right, since a restore exists to make the checkout match the lock. Replacing silently is not.
- Two paths pinning one artifact are two steps. One artifact pinned by name and by path has two destinations and is refused.

## What may satisfy a build dependency

A pin answers for its own keys. A build dependency only has to leave its dependent's equivalence key unchanged.

| Edge role | In the dependent's equivalence preimage | May be satisfied by |
|---|---|---|
| `data` | the dependency's equivalence key | anything carrying that equivalence |
| `app` | the dependency's `name/version` | that version, any build of it |
| `history` | nothing | any version of that name |

- The exact recorded identity is preferred in every case. It yields the exact identity for the dependent too.
- Where two dependents disagree, the stricter role wins.
- Under an `identity` project only the exact identity is accepted. A substitution would produce a dependent the mode rejects anyway.
- The recipe's `#DEP:` range is never consulted. It governed which version was first built against, and the manifest edge records that outcome.
- Restore reproduces recorded outcomes. It does not re-run authoring decisions.

## Scheduler submission

- A rebuild whose recipe carries scheduler directives is submitted. The job re-enters as `project restore --only <artifact>`.
- Nothing about the build is serialized into the job. The lock is the specification.
- The split is decided before the first step runs, because a dependency comes before its dependent.
- A build dependency is never its own job. Its directory cannot cross a job boundary, so the dependent's job produces it inline.
- `--keep-build-deps` makes it an ordinary step with its own directives. That helps when a dependency needs more of a machine than its dependent.
- A step is submitted when its recipe, or an inline build dependency, declares directives. Otherwise a heavy dependency would drag its light dependent onto the login node.
- A step whose dependency was submitted is submitted too, with an `afterok` edge.
- Each declared project path is one job.
- The submitting process reserves the target with an ordinary producer lock carrying the job ID. There is no job-state file.
- Re-running the restore is the resume. It reports live jobs, clears dead ones and resubmits, and adopts published output.
- A submitted restore job runs in the project root, or the submission is refused.

## The match mode is the project's

- Whether a substitute artifact is acceptable is a fact about the project. It is the lock's `match` field, and absent means `equivalence`.
- `Resolve` and restore both default from it, so `run`, `exec`, `check`, the dashboard and restore hold one project to one rule.
- An explicit mode still wins. `project registry push` passes identity, since it publishes exact artifacts.

## Acting in a project

- `run`, `check`, `exec` and a helper's required overlays act in the project the caller stands in. Standing elsewhere is the opt-out.
- `Standing` is the one implementation. Every caller finds the root, loads the lock and refuses the same way.
- Reads walk up to an ancestor project. Write commands do not.
- A write reaching an ancestor project from an empty-looking subdirectory is a hazard an ambient read never is.
- `--project DIR` relocates the whole invocation. `--no-project` disables lookup. They are mutually exclusive.
- A request classifies through `ParseDeclaration`, the grammar of `#DEP:`. A version constraint is refused, and a project path answers to the lock.
- `Standing` sits above `ResolveOverlayPaths`. Other callers there resolve a build's own `#DEP:` or a base image, and none may pick up the lock of the directory the user stands in.
- Resolve never acquires. An absent artifact is an error naming `project restore`.
- A `name:` request is held to an exact identity when it is pinned, not when it runs.
- With no pin, `exec -o`, `run` and `check` resolve as they would outside a project: an installed version first, then the catalog's candidates, never a build.
- A live-resolved mount is marked, and each caller prints what it resolved to.
- The project's root is the exception. It is fixed, never live.

## Verification

- `Verify` is checkout-local: no installed overlay, store, catalog, configuration, network or payload.
- A fresh clone is either a complete, consistent rebuild specification or it is not.
- Entries are regenerated, not trusted. The recorded keys are recomputed from the vendored sources.
- The directory name is addressing, never identity. Renaming a directory cannot make a different artifact answer to it.
- Each dependency edge is followed by name and complete identity, to a child that agrees on both.
- Reachability is the closure, not the pin set.
- Problems are collected, not raised at the first, because someone fixing a lock wants the whole list.
- A lock is untrusted input. Reads are bounded and refuse symlinks, and every path in it must be relative, clean, slash-separated and non-escaping.

## Publication

- Publish marshals, writes a sibling, renames, then prunes. A failure before the rename leaves the old lock authoritative.
- Remotes are reconciled before marshalling, so the written bytes never name an artifact the closure no longer reaches.
- A re-pin at a new identity leaves the old directory until Publish prunes it.
- An artifact directory is staged as a dotted sibling and renamed in. An existing entry is never rewritten, since one name and identity has one set of records.
- Dotted names are invisible to the listing, so a write in progress is not a validation problem.
- Prune leaves unreadable entries alone. Deleting on a read error would turn a corrupt file into data loss.
