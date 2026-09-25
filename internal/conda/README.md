# internal/conda

## In-container isolation

- `CNT_CONDA_ROOT` identifies the mounted environment. Mutations also need `CNT_CONDA_WRITABLE=1`.
- Micromamba is found with `toolpath.Resolve`, not a fixed path. A base image is not required to carry one.
  - libexec's provisioned copy is found first.
  - That works only because `Setup` binds `libexec.Dir()` into the container when an environment is mounted.
  - This package cannot make that bind itself, since it already runs inside the container.
- `CONDA_PREFIX` and `MAMBA_ROOT_PREFIX` are reset to the mounted environment for every command.
- Ambient Conda and Micromamba prefix and rc variables are removed.
- With `/cnt_env/.condarc`, Micromamba runs with `--rc-file`. Without it, `--no-rc` during creation.
- The first install uses explicit project channels, then saves them to `.condarc` after success.
- User arguments are forwarded unchanged. Micromamba owns transaction locking.

## Reactivation

- `ReactivateScript` only returns text for the caller to `eval`.
  - A subprocess can never reach back into its parent's shell, so re-running `activate.d` is the caller's act.
- It mirrors conda's own reactivate: deactivate scripts in reverse order, then activate scripts in forward order.
- It is its own small implementation. `internal/conda` cannot import `internal/runtime/container`, because `container` depends on `internal/artifact/key`, which depends on `conda`.
- It takes a `Shell` because fish needs different syntax, not different words.
  - Fish has no `VAR=val cmd` form, so `CONDA_PREFIX` is scoped with a `begin; set -lx ...; end` block.
  - Fish also changes which hooks are considered. Conda uses `.fish` for fish and `.sh` for everything else.
  - A bash-only hook is skipped under fish instead of being fed to fish's parser as invalid syntax.

## Reading a paired image

- The host-side reads take exactly the path they are given. They never decide whether a pairing applies.
- A writable `.img` paired with a frozen snapshot holds only the newest delta in its own `conda-meta`.
  - Reading it alone reports most of the environment as missing.
- Knowing whether a path has a pair needs `container.LookupSnapshot`, which this package cannot import.
- `container.PairedPackages` and `PairedInfo` make that decision and call the right function here. Callers use those.
- `ReadCondaInfoMerged` treats the two histories as one log, since the `.img`'s is the continuation of the snapshot's.

## The two exports

- A Conda app embeds `explicit.txt` and `environment.yml`, captured from the environment that was installed, not from a second solve.
- Together they are the artifact's identity and equivalence. `sha256sum` on either reproduces a key by hand.
- Both are re-emitted, never stored as the tool printed them.
  - A Micromamba upgrade that reorders or re-spaces output would otherwise move every later key.
  - Every comparison across that boundary would report a difference with an empty diff.
  - Sorting is safe. Conda does not depend on explicit-file order.
- `name:`, `prefix:` and a trailing `#<md5>` are dropped. They describe a local environment or the download, not the packages.
- The channel set is the export's. Only the order is corrected, into the configured priority.
  - A channel used but not configured, such as a mirror, is appended after the known ones, not dropped.
  - Adding an unused channel to a site's config changes no key.
- The asymmetry is intended. A build string moves identity but not equivalence. A version moves both.
  - `numpy=1.26.4` built against MKL and OpenBLAS are equivalent, and identity tells them apart for anyone who needs it.
- pip installs are not supported. A centrally managed overlay does not carry them.
  - `--explicit` omits pip packages, so the identity ignores them.
  - A `pip:` sub-list in `environment.yml` is preserved and sorted, and nothing resolves or verifies it.
  - A restore of the image keeps the pip files, since they are in the payload. Anything rebuilt from the exports may not.
