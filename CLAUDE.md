# CLAUDE.md

CondaTainer is an HPC CLI that packs Conda environments into single image files (SquashFS/OverlayFS) via Apptainer and Micromamba, to avoid inode quotas.

## Commands

```bash
make build          # bin/condatainer_go, CGO_ENABLED=0 (bind-mounted into old-glibc containers)
make test           # go test ./...
go test -v ./internal/build -run TestParseScriptMetadata
```

- Tests skip when `mksquashfs`, `squashfuse` or `fuse-overlayfs` are missing. Put the libexec `bin` on `PATH` to run them.
- Tests that read config call `testenv.Isolate()` in `TestMain`. The root dir is cached, so isolating later is too late.
- `golang.org/x/crypto` is replaced by `github.com/condatainer/x-crypto`. A workflow bumps it.

## Architecture

- CLI: cobra + viper. `main.go` calls `cmd.Execute()`. One file per subcommand in `cmd/`, delegating to `internal/`.
- `catalog/`: resolves a name to a recipe and a set of names to a build order. Owns the `#KEY:` header tokenizer.
- `internal/artifact/`: what an image embeds. Identity, manifests, equivalence comparison.
- `build/`: name/version to a Conda, Script or Def build. Dependency graphs, remote scripts.
- `conda/`: the Conda environment mounted at `/cnt_env`.
- `config/`: layered config (flags > env > user > extra-root > app-root > system > defaults) and data directory search.
- `helper/`, `helperhistory/`: helper service jobs and their shared run history.
- `image/`: overlay CRUD (ext3, SquashFS, sif), freeze, resize, locking.
- `libexec/`: self-provisioned toolchain (`mksquashfs`, `squashfuse`, `fuse-overlayfs`, `apptainer`) via micromamba.
- `logging/`: context-carried logger and raw-output writer.
- `project/`: `cnt-lock/`, a project's pinned identities and vendored recipes.
- `registry/`: OCI transport. Publish, resolve, verify, pull, login.
- `runtime/`: `apptainer/` wrapper, `container/` setup, `exec/`, `proxy/` for compute nodes.
- `scheduler/`: SLURM, PBS, LSF, HTCondor. Detection, directives, translation.
- `server/`: dashboard HTTP server. `store/`: immutable overflow store, addressed by name plus key.
- `toolpath/`: finds a runnable path for a host tool. `utils/`: console output, files, downloads, script parsing.

## Domain rules

**Root and layering.** Any `os` overlay can be the container root at runtime.

- It is a per-invocation choice, not a declared type.
- `config default_distro` names an ordinary `os` artifact.
- Apptainer and Micromamba come from `internal/libexec`, never from the root.
- Two overlays claiming one `/cnt/<name>` prefix is refused, not merged.

**Publishing.** What may be pushed depends on who can pull it.

- Each endpoint declares `audience`: `public` (default) or `restricted`.
- The decision reads the embedded manifest, never the filename.
- `push` refuses. No flag overrides a refusal.

**Data directories.** Reads go nearest-first and writes furthest-first: scratch, user, extra-root, app-root.

**Locking.** `exec`/`run` hold a shared `fcntl` lock on `.sqf`/`.sif` files. `remove` and `build --update` probe an exclusive lock first.

- An unwritable image is protected. It is never modified or removed. `chmod a-w` pins one.
- A write lock opens `O_RDWR`, not `O_RDONLY`.

## Code rules

- **Errors: sentinels by default.** A package-level `ErrX`, wrapped with `%w`, matched with `errors.Is`.
- Use a struct only when a caller must read a field (`apptainer.ApptainerError`, `scheduler.SubmissionError`).
- Console output: `utils.PrintMessage`, `PrintWarning`, `PrintError`, `PrintDebug`.
- Use absolute paths and `config.Get*Dir()` for standard locations.
- Create files and dirs with `utils.CreateFileWritable`, `MkdirAllShared`, `MakeExecutable`. Never a bare `os.Chmod(path, utils.Perm*)`.
- Global config singleton: `config.Global`.
- After editing Go files, run `gofmt -l .` and fix what it lists with `gofmt -w`.

## Writing rules

Three places, three jobs.

**Comments say what the code does.**

- Behavior first. Give a reason only where the behavior would look wrong without it, in a clause.
- A function comment over 6 lines is a mistake, unless the function branches and each branch decides something.
- Self-contained. Never point at a README, a plan, a doc page or another file.
- No measurements, hosts or incidents. State the rule the incident taught.
- No comparisons to other tools.
- Never argue with an objection nobody made.
- Present tense only. No "previously", no "no longer".

**CLI help and user docs say what a user does and sees.**

- Commands, flags, output, what is refused and how to proceed.
- Never a design argument or how it works inside. State the rule and its consequence.
- Cobra `Long` replaces `Short` in `--help`, so `Long` must stand alone. No `Example` on group commands.

**A package `README.md` records decisions.**

- The decision, the constraint behind it, and orderings that are load bearing.
- Never a description of what the code does. It cannot be kept in sync.
- External limits and the values chosen against them belong here, with the reason.
- Short bullets. One fact per bullet, one short sentence each.

## Editing

Every comment, doc and README describes the present.

- Edit the part that is now wrong. Never add a note about the old behavior or why it changed.
- No "previously", no "now", no correction layered on a correction. Git holds the history.
- A section that grew by accretion is rewritten, not appended to.

When code changes, update everything that describes it in the same change.

- The function comment.
- The package `README.md`, if a decision or a limit changed.
- CLI help and user docs, if a flag, output or refusal changed.
- Tests, per the rules below.

## Tests

- A test names behavior that exists now. When behavior changes, delete the tests for the old behavior.
- A removed special case takes its tests with it. A general rule that replaces several cases gets one test.
- A change that removes behavior leaves fewer test lines than it found.
