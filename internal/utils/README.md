# internal/utils

## File lock

- `AcquireFileLock` is an `fcntl` lock, not `flock`, because Apptainer uses `fcntl` on a mounted ext3 image.
  - `flock` conflicts with an `fcntl` lock only on filesystems that fold the two together. NFSv3 does, NFSv4 does not.
- A running `.img` is left to Apptainer, which locks it itself. Only `.sqf` and `.sif` are locked here.
- A write lock opens `O_RDWR`, because an exclusive `fcntl` lock needs a writable descriptor.
  - An image with its write bit cleared is therefore protected.
- A shared lock opens `O_RDONLY`, so a protected image can still be read.

## Header lines

- `ScanAnnotations` is the one place a `#KEY: value ## note` line is tokenized.
- Recipes, user scripts, helper scripts and project scanning all read through it, so a key means the same everywhere and adding one happens once.
- A package owns the meaning of its own keys. This package knows the shape of a line and nothing about what a key does.
- Position carries no meaning. There is no header block and nothing bounds the scan.
- A recipe that writes a job script in a heredoc therefore also declares whatever that script declares. Nothing distinguishes the two without parsing shell.
- A value ends at the first `##`, which starts a comment. A single `#` is part of the value, so a URL fragment or a colour code survives.
- An upper-case key keeps prose out: `# note: rerun weekly` is a comment, `#DEP: star/2.7.11b` is a declaration.
- Scheduler directives are not annotations. `#SBATCH --time=01:00:00` has colons in its value, so they are matched by prefix and handed to the scheduler packages verbatim.

## Comments

- `StripComments` is what the keys hash. It removes whole-line comments, so editing a comment moves neither key.
- A trailing comment cannot be removed without parsing shell, since `echo "a # b"` and `${v#pre}` both carry a `#` that is not one.
- Neither function touches what is stored or what runs. `/.cnt/recipe` is the original bytes.

## Value lists

- `ParseValueList` reads the list of a `#PH:` or `#VALUE:` header.
- The separator sets the order. A comma sorts newest first, a pipe keeps the order written, and `*` is always last.
- `values[0]` is the default offered to users.

## One spelling of a module name

- `NormalizeModule` folds `name=version` and `name--version` into `name/version`.
- A constraint suffix (`>=`, `>`) survives. A trailing `@<identity>` survives, since `@` separates a name from an identity and never a version.
- Index keys use this form. A second implementation elsewhere would make one string resolve two ways.
- `ParseDep` takes the last path component as the version, so a template dependency keeps its token there.
- The preferred version is the implicit upper bound of a dependency. `2.7.0` as the minimum with `2.7.11b` as the version admits `[2.7.0, 2.7.11b]`.
- With no constraint, a version admits any version that shares it as a dot-component prefix. `17` admits `17.0.18`, the family match conda gives a bare `=17`.
- These parsers stay in this package while they are small. If they grow, they split into a subpackage of `utils`.

## Imports

- `utils` imports no other condatainer package, so any package may import it.
- Parsers that carry no domain knowledge live here: durations, memory sizes, versions.
- A reader of a recipe or script header is domain knowledge and lives in `catalog`.
