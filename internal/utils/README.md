# internal/utils

## Imports

- `utils` imports no other condatainer package, so any package may import it.
- Parsers of text conventions that many packages share live here: durations, memory sizes, versions, header lines and module names.
- A reader that needs a package's own rules stays in that package. Recipes are `catalog`'s, build rules are `build`'s.
- The header and name parsers stay in this package while they are small. If they grow, they split into a subpackage of `utils`.

## File lock

- `AcquireFileLock` is an `fcntl` lock, not `flock`, because Apptainer uses `fcntl` on a mounted ext3 image.
  - `flock` conflicts with an `fcntl` lock only on filesystems that fold the two together. NFSv3 does, NFSv4 does not.
- A running `.img` is left to Apptainer, which locks it itself. Only `.sqf` and `.sif` are locked here.
- A write lock opens `O_RDWR`, because an exclusive `fcntl` lock needs a writable descriptor. An image with its write bit cleared is therefore protected.
- A shared lock opens `O_RDONLY`, so a protected image can still be read.

## Header lines

- `ScanAnnotations` is the one place a `#KEY: value ## note` line is tokenized. Recipes, user scripts, helper scripts and project scanning all read through it, so a key means the same everywhere.
- A package owns the meaning of its own keys. This package knows the shape of a line and nothing about what a key does.
- Only the header block declares: the leading blank and comment lines, up to the first line of code. A later line is ignored and warned about, never silently dropped.
  - A recipe that writes a job script in a heredoc declares nothing of that script's.
  - Schedulers read their directives the same way, and `HeaderEnd` is the one place the boundary is decided.
- A value ends at the first `##`, which starts a comment. A single `#` is part of the value, so a URL fragment or a colour code survives.
- An upper-case key keeps prose out: `# note: rerun weekly` is a comment, `#DEP: star/2.7.11b` is a declaration.
- Scheduler directives are not annotations. `#SBATCH --time=01:00:00` has colons in its value, so they are matched by prefix and handed to the scheduler packages verbatim.
- `StripComments` is what the keys hash. It removes whole-line comments only, since a trailing `#` may belong to shell (`echo "a # b"`, `${v#pre}`). It never touches what is stored or what runs.
- A flag header is on when its line is present. `Flag` accepts no value or `true`, so `false` cannot read as on.
- `Reject` names the first listed key a script writes. A consumer calls it for the shared headers it does not honor, so there is no table of script kinds.
- `ParseValueList` reads a `#PH:` or `#VALUE:` list. A comma sorts newest first, a pipe keeps the order written, `*` is always last, and `values[0]` is the default offered.

## Module names

- `NormalizeModule` folds `name=version` and `name--version` into `name/version`. Index keys use this form, so a second implementation would make one string resolve two ways.
- A constraint suffix (`>=`, `>`) and a trailing `@<identity>` survive. `@` separates a name from an identity and never a version.
- `ParseDep` takes the last path component as the version, so a template dependency keeps its token there.
- The preferred version is the implicit upper bound. `2.7.0` as the minimum with `2.7.11b` as the version admits `[2.7.0, 2.7.11b]`.
- With no constraint, a version admits any version that shares it as a dot-component prefix. `17` admits `17.0.18`, the family match conda gives a bare `=17`.
- `ScriptDependencies` reads a script's `#DEP:` lines with that grammar. A name is normalized, and an overlay path (`IsPathDep`) is kept as written.
- `run`, `check` and a build all read through it. What a dependency may be is each caller's rule: a running script may name a path, and a build may not.
