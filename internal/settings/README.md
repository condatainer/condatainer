# internal/settings

The registry of config keys. A package declares the keys it reads, and reads them through typed handles.

## Decisions

- A key is declared in the package that reads it, so adding one is one declaration there.
  - This package holds the machinery and declares no key.
- It imports only `utils`.
  - `config` imports it to feed the layer files in, and every owner imports it to declare keys.
  - `config` cannot be imported by `catalog` or `scheduler`, so the registry cannot live in it.
- A default is text parsed by the key's kind, so it cannot disagree with what `config set` accepts.
- A declaration that can never work panics at registration: a bad name, a missing `Help`, a default the kind rejects, a duplicate, two keys with one variable name, or a key that is also a section.
  - Registration is by package initialization, so any test run finds them.
- A default that depends on the environment is a function, read each time and never cached.
  - `build.logs_dir` follows `$SCRATCH` and `HOME`, so a replaced `HOME` moves it without any hook.
- `Validate` rejects a value the kind accepts but the key does not, such as a block size that is not a power of two.
- `MergeLists` makes a list the union of every layer, highest first and each element once. `Resolution.Origins` names the layer of each element.
- A list key reads its environment variable split on `|`, or on `:` when there is no `|`. The first flag element replaces the config list.
- `Get()` before any layer is installed returns the default. The in-container commands skip config and must stay safe.
- Precedence is flag, `CNT_CONFIG_*`, layers (user, extra-root, app-root), default. An empty variable counts as unset.
- A value a kind rejects is reported once and skipped. The next source applies, ending at the default.
  - A bad line in a config file never stops a command.
- Layers are an interface, not config's file type, so the resolver is tested without files.
- A flag is a separate layer filled at argument parsing. Only a flag the user passed counts.
  - Two different flags for one key in one command are an error.
  - It serves the command-line process only. A long-lived server passes a per-request value as an explicit option.
- `OverrideValue` is the flag layer used from tests. Tests that use it must not run in parallel.

## Renames and removals

- A rename is declared on the new key with `Replaces(old, Since(v))`. The old name reads as the new key in its own layer, so layer priority holds.
  - In one layer the new name wins, and `config check` reports the stale one.
  - `CNT_CONFIG_<OLD>` is read the same way, and the new variable wins when both are set.
  - `Migrate` converts a value whose shape changed.
- A removed key is declared with `Removed(name, message, Since(v))`. Its value is reported once and ignored.
- `Deprecated(message, Since(v))` marks a key that still works. Using it warns once.
- `Since` is required. `RemoveIn(v)` is checked by a test that fails once the release reaches it, so the line gets deleted.
- A shared config may be read by several versions at once.
  - `config check --fix` needs `-l` and rewrites one layer's file per run.
  - `--keep-old` writes the new name and leaves the old one, so an older version still reads it. A later `--fix` drops it.
  - An alias is deleted only after every version in use knows the new name.
- Names never collide: an old name, a removed name and a live key cannot share a name or a variable.
- Before the first release no alias is declared. The rule once released is that a rename or removal adds `Replaces` or `Removed` in the same change.

## Kinds

Each kind owns how text is parsed and normalized, what it accepts, and what completion suggests.

| Kind | Reads as |
| --- | --- |
| `Bool` | `bool`; `true`, `false`, `1`, `0`, `t`, `f` |
| `Int` | `int`, with `Min` and `Max` |
| `Days` | `time.Duration` from whole days |
| `MemoryMB` | `int64` megabytes; the text is stored as typed |
| `Walltime` | `time.Duration`; the text is stored as typed |
| `Enum` | `string`, one of `Values` |
| `Path` | absolute `string`, environment expanded on read |
| `String` | `string` |
| `List` | `[]string` |
