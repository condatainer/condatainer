# internal/report

The JSON an overlay command prints with `--json`.

## Document

- One document on stdout, always an object, so a field can be added later.
- It opens with `schema_version`. A field is added without a bump. A rename or removal bumps it.
- The list is keyed by the kind of record it holds, not by command: `steps`, `overlays`, and so on. Commands returning the same kind share the key.
- `problems` is in every document, `[]` when empty, so a reader never tests for its absence.
- An empty list is `[]`, never `null`.

## Records

- Every record about one overlay starts with `name`, `type` and `arch`, whichever command prints it.
- They stay flat even when a nested document, such as `runtime`, repeats them. The duplicate is on purpose: a reader finds them in one place everywhere.
- A record carries everything the command knows about it. Nothing is shortened for display.
- Every record with a file path says the file's `format`, even where every file is SquashFS today. A command may handle another kind later without a version bump.
- A key is always full, `{scheme, sha256}`. Only a tag holds a short key, because that is all a tag has.
- An optional field is omitted when empty. A boolean that decides something, such as `requires_input`, is always written.
- Enums are lower-case strings, so a new value never shifts an old one.
- HTML characters are not escaped: paths and placeholders such as `<timestamp>` print as written.

## Imports

- Only `artifact`, `meta` and `store`, for the types records reuse, and `utils` for the format rule. Never `cmd` or `project`, so every command can import it.
