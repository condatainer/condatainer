# internal/config

## Keys

- This package declares only the keys nothing else owns: `default_distro`, `home_override` and `metadata_cache_ttl`.
- It finds and reads the layer files and hands them to `internal/settings`, which resolves every key.
- `config.Global` holds runtime facts that are not keys: the version, the program directory, the debug flag, the resolved sources.

- `config check` is a command and not a startup warning.
  - A typo in a file is otherwise ignored, but a warning on every command costs every user and gets learned and ignored.
  - A bad value still warns once per run.
- `config set` refuses an unknown key. Reading a file ignores one, because a shared install can be read by two versions at once.

## Layering

- Environment variables and config files are two separate mechanisms.
- A `CNT_CONFIG_*` variable is admin-level control, for example in a module file. It replaces the config value for that key entirely.
- Config files are layered, and every existing file is loaded.
  - Scalar keys: the highest-priority file that sets it wins.
  - `sources`: merged across layers, deduplicated, user entries first.
  - `bind`: merged the same way, so a site or lab adds binds and a user adds more.
  - `channels`: not merged. The highest-priority file that sets it wins.
- Priority, highest first: flags, `CNT_CONFIG_*`, user, extra-root, app-root, defaults.
- A layer file is read and written through `yaml.v3` nodes, in `layer.go`.
  - A write edits the node in place, so comments and key order survive a `config set`.
  - It happens in the open file under an exclusive lock, never through a temporary file and a rename. A shared config is owned by one user and group-writable, and a rename would give it to whoever wrote last.
  - A key is read nested or flat, case-insensitively, and a null value counts as unset.
  - A scalar is converted by the reader: a string `1` reads as a true boolean, and a list key written as a plain string splits on whitespace.
- There is no `/etc` layer. On a cluster `/etc` is per node, so a value there would differ between the login node and a job.

## Recipe sources

- `sources` is an ordered list. Each entry is a single-key mapping, `- lab: /shared/lab/recipes`.
  - Order and handle are both load-bearing, and a plain map gives neither.
- `config source` is the only way the list is edited. The generic array commands refuse `sources`.
  - Adding a source reads it first, so a wrong URL or a missing token is caught before anything is saved.
  - A token goes to the layer's `credentials.json`, never `config.yaml`, which a shared layer's users must all read.
  - There is no environment override. One would bypass layers, the default and the token store.
- Placement is within a layer only. Layer order is the precedence everyone in a group shares, and a person must not reorder a group's list from their own.
  - A new source goes last, so adding one never changes what already resolves.
- The public `cnt` collection is a default value, not a fallback the resolver reaches for.
  - It is appended, so every configured entry outranks it.
  - A site that defines `cnt` replaces it. That is how the handle points elsewhere without rewriting the `#DEP:` lines that name it.
  - Moving the default writes it into a layer, after which it is an ordinary entry. Removing that entry returns the default.
- Tokens are attached only when the catalog opens. `Global.Sources` is printed by `config list` and must never carry one.
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

## Home override

- `home_override` replaces `HOME` for clusters whose home is read-only on a compute node.
- It is a setting, not a probe. A probe would write state to scratch on a compute node and read it from `$HOME` on the login node.
- `HOME` is replaced once, right after the config layers load.
  - Everything that follows `$HOME` follows it: the data, cache and state directories and every child process.
  - Apptainer's container home does not. It is the passwd home, so a container gets `--home <replacement>`.
  - A child that sets its own `HOME`, such as the toolchain provisioning, keeps it.
- The config file, credentials and the install-root exclusion stay on the real home.
  - The key is read from them before the replacement, and they are written on a login node.
- The real home is saved in `CNT_REAL_HOME` and read back with `utils.RealHome`.
  - A job or a nested call inherits the replaced `HOME`, so `$HOME` no longer says where the config is.
  - Replacing again keeps the saved home.
- A container binds the real home read-only at its own path.
  - A user's symlink from the replacement home into the real home resolves inside it.
  - `CNT_REAL_HOME` passes into it, so a nested call finds the user's config and credentials there.
  - Condatainer creates no symlinks. The replacement home starts empty.
- A job script replaces `HOME` itself in its header.
  - It does not depend on the scheduler copying the submit environment.
- Scheduler commands run with the real home.
- Paths chosen at submit time are written into the script, so the submitting process must already resolve them under the replacement.
- SSH never reads `$HOME`. Both the client and the `ssh` binary take the home from the passwd entry.
- An unusable value warns and leaves `HOME` alone, so `config set` can still repair it.

## Reload

- A long-lived process (the dashboard server) re-reads the config files when one is created, edited or removed. The CLI reads them once.
  - The check compares each file's modification time and size, so it needs no file watcher and works on shared filesystems.
  - A file that does not parse leaves the previous config in place.
- Recipe sources are reopened on a reload. `home_override` and the scheduler are applied once at startup and are not re-applied.
