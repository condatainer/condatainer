# catalog

Resolves a module name to a recipe.

- It is a getter and a name solver. It never builds, never chooses and never talks to conda.
- A caller decides what to do with a result.
- A collection is a directory or a URL holding `recipes/`, `index/`, `helpers/` and a `source.json` descriptor.
- Configured sources are ordered, and the first match wins, like `PATH`.

## The boundary

- Conda is absent. A name no source provides is not an error. It comes back with a nil entry and its constraint intact, for the caller's conda fallback.
- Nothing pre-solves conda. Micromamba solves against the whole environment, and a second opinion could only agree or be wrong.
- The scheduler is absent too. Recipes read normalized `$NCPU` and `$MEM`, which the scheduler package produces.
- `catalog` and `scheduler` never import each other. `build` and the commands import both.
- `catalog` never imports `config`, which imports it to open the recipe sources.

## Resolving a raw name

- `Lookup` needs an exact index key. `SolveName` extends that to what a person types: a bare name, a partial version, or a missing distro prefix.
- It tries `raw` as given, then `distro/raw` once the first came up empty. Using the name as typed is not a guess. Prepending a distro is.
- Each attempt stops at the first of three steps: a complete module (an exact key, or a template with its placeholders filled), the app pool, then the os pool.
- Installed beats a catalog lookup entirely. When `have` answers, the catalog is never consulted, so an overlay keeps resolving after its recipe moved or was dropped.
- The newest app candidate is safe to pick, because an app's candidates are the same tool at different times.
- An os candidate is only a template's own declared version list. Its second component names a different app, not a version.
- Data gets no autofill. Sibling entries under `grch38/genome` are alternative sources, not versions, so comparing them would compare arbitrary strings.
- A bare distro name has no versions. `create ubuntu24` reports not found instead of guessing.
- `have` and `distro` are parameters, never ambient state, so a name means the same thing for every caller and on every machine.

## Recipes

- `Recipe.Validate` returns problems and never prints. It runs when a recipe is fetched for a build, not while indexing, so one bad recipe stops its own build instead of taking a collection out of every listing.
- Nothing lints a near-miss glued version such as `star2.7.11b`. A data dependency's name is often several components deep, and requiring all of it to reappear would false-positive on ordinary compact naming.
- Expanding a template leaves `Text` alone. `Expand` sets `Rendered`, the copy a build runs. `Text` is what an artifact embeds, so every variant shares one recipe digest, told apart by the recorded placeholder values.
- Placeholder key order is not stored. It derives from the `#TARGET:` token order. Value order is stored, because `values[0]` is the default offered.
- A template match is not a wildcard. Names match the declared `#PH:` values, and only an explicit `*` is permissive. A half-filled name determines nothing.
- Value lists sort once, where the separator is visible. A comma sorts newest first and a pipe keeps the author's order. An HTTP source takes the published values verbatim, since the index records no separator.

### `#SOURCE:`

- `#SOURCE:` declares a file the build fetches: a name, an optional architecture, and either one URL or `ask:` and a prompt.
- A URL and a prompt expand with the template. The name survives expansion unchanged, and the identity records the fetched digest against it.
- The architecture is `amd64` or `arm64`, Go's spelling. Vendors spell architectures too many ways for a placeholder, so each architecture is a literal line.
- A name is all qualified or all plain, so "which wins" never depends on declaration order. A name with lines only for other architectures is an error, not an unset variable.
- `Recipe.Prompts` is one ordered list: the `#INPUT:` prompts, then each `ask:`. Answers split by count alone.
- A malformed `#SOURCE:` is skipped at parse and reported by `Validate`. At build time it is an error, since the body would otherwise run against an unset variable.
- A definition declares neither `#SOURCE:` nor `#INPUT:`, because nothing would fetch or read them.
- The header is a comment, so changing a URL moves no key. Only the bytes it served can, through the recorded digests.

## Sources

- A downloaded file skips the cache. The cache holds indexes, where a stale one beats none. A script is fetched to be installed, so `ReadFresh` returns what is served now or fails.
- An unreachable source keeps its place. Dropping it would let first-wins promote the next source's recipes while the build looks normal.
  - It stays in the list carrying its error. Lookups skip it and the caller reports it once.
  - A catalog where every source failed is empty, not an error.
- A stale index beats a failed fetch. An expired entry with no route out is served with a flag set. That is a compute node, not an error.
- A private HTTP source is one read with a token, tested on GitHub's raw host only.
  - No token on a private repository gives 404, as does a wrong URL. A bad token gives 401, retried once without it, and a refusal after that is reported as the token's.
  - A host outside the host table is tried with the standard Bearer scheme.
  - The cache is keyed by URL, with files `0600` in `0700` directories.
- The selected source owns its registry.
  - `source.json` may declare one OCI `registry` and an `audience`. Push and pull use the same one.
  - A build keeps the exact source that won first-match lookup and tries only its registry.
  - A pulled artifact must match the equivalence key derived from that same recipe before it is installed.
  - An invalid descriptor disables its registry without hiding its recipes.

## Invariants worth not breaking

- There is one spelling of a name. `utils.NormalizeModule` decides it, and index keys use that form. A second implementation would make one string resolve two ways, and the same holds for `CompareVersions`.
- The two source backends must return identical entries for the same collection. A test builds one on disk, serves it over HTTP and diffs the results. That test is the contract.
- Every format a source serves carries a required `schema_version`: `source.json` and both index files.
  - An index wraps its entries in an object, since a bare object keyed by name has no room for a version.
  - A version this build does not read is refused, and a newer one says to update.
  - A key a reader does not know is ignored. The version rises only for a change an older reader would misread.
- The caller owns the cache directory and TTL. The package owns everything inside it.
