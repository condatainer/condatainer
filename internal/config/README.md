# internal/config

## Layering

- Environment variables and config files are two separate mechanisms.
- A `CNT_*` variable is admin-level control, for example in a module file. It replaces the config value for that key entirely.
- Config files are layered, and every existing file is loaded.
  - Scalar keys: the highest-priority file that sets it wins.
  - `sources`: merged across layers, deduplicated, user entries first.
  - `bind`: merged the same way, so a site or lab adds binds and a user adds more.
  - `channels`: not merged. The highest-priority file that sets it wins.
- Priority, highest first: flags, `CNT_*`, user, extra-root, app-root, system, defaults.

## Recipe sources

- `sources` is an ordered list. Each entry is a single-key mapping, `- lab: /shared/lab/recipes`.
  - Order and handle are both load-bearing, and a plain map gives neither.
  - `CNT_SOURCES` overrides the whole list.
- The public `cnt` collection is a default value, not a fallback the resolver reaches for.
  - It is appended, so every configured entry outranks it.
  - A site that defines `cnt` replaces it. That is how the handle points elsewhere without rewriting the `#DEP:` lines that name it.
- An unreachable source is reported, not fatal.
  - The rest still answer, and a compute node with no route out is ordinary.
  - It cannot be silent either. Sources are first-wins, so an unreachable one promotes the next source's recipe or falls through to conda, and the build looks normal.
  - `WarnUnreachableSources` warns once per process. Call it after the catalog has been used, since a source's error is set when it is first read.
- `-s` on `avail` and `create` restricts the list. Flag order becomes lookup precedence.
  - Dependencies and the default distro use the same restricted catalog.

## Default distro

- It is recorded once by `config init` and never revised.
  - Changing it rebuilds the container root and every `os` overlay stacked on it.
  - Following an upstream bump would invalidate a whole set of images on an ordinary update.
  - A later default is something the user opts into with `config set default_distro`.
- Commands other than `config`, `help`, completion and the hidden ones refuse to start without one.
- `ResolvedDefaultDistro` reads config only and never opens the catalog.
  - It prefixes bare installed names on offline paths like `list` and `info`.
  - Opening a source there would mean a network fetch to expand a local name.

## Data directories

- Reads go nearest-first and writes go furthest-first. Order within a tier is the same both ways.
- A build lands as far out as permissions allow, so one copy serves the whole group.
- A personal build shadows a shared one for the person who made it.
- Tiers, in read order: scratch, user, extra-root, app-root.
- Recipes are not searched here. They come from `sources`.
- The toolchain is not searched either. It is one directory, owned by `internal/libexec`.
- A display command peeks at the write target and never creates it. Only a write creates a directory.
- Writes go to the first writable directory in reverse order.
  - Personal dirs are created on first use.
  - A shared dir gets its subdirectories only when its parent already exists. The parent is never created.
- The cache is always written to a personal directory. A shared dir is never written to, to avoid cross-user pollution.
- `GetWritableTmpDir` is the stable root for long-lived work. `CNT_TMPDIR` does not redirect it.
- A data layer is a choice made at the command, not a path. Its meaning is local to the machine, so it is never recorded.
