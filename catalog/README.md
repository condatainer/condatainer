# catalog

Resolves a module name to a recipe, and a set of names to a build order.

- It is a getter and a dependency solver. It never builds, never chooses and never talks to conda.
- It is shared between two tools, so anything opinionated about what to do with a result stays out.
- A collection is a directory or a URL holding `recipes/`, `index/`, `helpers/` and a `source.json` descriptor.
- Configured sources are ordered, and the first match wins, like `PATH`.

## The boundary

- Conda is deliberately absent.
- A name no source provides is not an error. It is the normal path for a conda package.
- The node comes back with a nil entry and its constraint intact, for the caller's fallback.
- Nothing pre-solves conda. Micromamba solves against the whole environment, and a second opinion computed beforehand could only agree or be wrong.
- The scheduler is absent too. Recipes read normalized `$NCPUS` and `$MEM`, which the scheduler package produces.
- The edge runs from each tool to both packages, never between them.

## Resolving a raw name

- `Lookup` and `Resolve` need an exact index key. `SolveName` extends the same boundary to what a person types: a bare name, a partial version, or a missing distro prefix.
- It makes two attempts. First `raw` as given. Then `distro/raw`, only once the first came up empty and `raw` has at most one slash.
- Using the name as typed is not a guess. Prepending a distro is.
- Each attempt has up to three steps, and stops at the first hit.
  - A complete module: an exact key, or a template with its placeholder filled. A bare template names a family, not a module, so it falls through.
  - The app pool, for names with at most one slash.
  - The os pool, for names with at most two slashes.
- Installed beats a catalog lookup entirely, not just a newer version. When `have` answers, the catalog is never consulted.
- That also keeps an overlay resolving after its recipe moved or was dropped. `Resolved.Entry` is nil then.
- The newest app candidate is safe to pick. An app's candidates are the same tool at different points in time.
- An os candidate is only a template's own declared version list. An os entry's second component names a different app, never a version of the first.
- Data gets no autofill.
- `grch38/genome` runs out of steps. The sibling entries under it (`ucsc`, `ensembl`) are alternative sources, not versions, so comparing them would compare arbitrary strings.
- A fully typed data name still resolves through the first step. Only the choice among data siblings is refused.
- A bare distro name has no versions. `create ubuntu24` reports not found instead of guessing.
- `have` and `distro` are parameters, never ambient state.
- That makes a name mean the same thing for every caller, and stops it resolving differently on different machines.
- It also means no caller needs to import back into `catalog` to supply them.

## The header boundary

- `ScanAnnotations` is the one place a `#KEY: value ## note` line is tokenized.
- Recipes, user scripts and project scanning all read through it, so a key means the same everywhere and adding one happens once.
- Position carries no meaning. There is no header block and nothing bounds the scan.
- A recipe that writes a job script in a heredoc therefore also declares whatever that script declares. Nothing distinguishes the two without parsing shell.
- An upper-case key keeps prose out: `# note: rerun weekly` is a comment, `#DEP: star/2.7.11b` is a declaration.
- Scheduler directives are not annotations. `#SBATCH --time=01:00:00` has colons in its value, so they are matched by prefix and handed to the scheduler packages verbatim.
- `StripComments` is what the keys hash. It removes whole-line comments, so editing a comment moves neither key.
- A trailing comment cannot be removed without parsing shell, since `echo "a # b"` and `${v#pre}` both carry a `#` that is not one.
- Neither function touches what is stored or what runs. `/.cnt/recipe` is the original bytes.

## Validation

- `Recipe.Validate` returns problems and never prints. Nothing here writes to a terminal.
- It runs when a recipe is fetched for a build, not while indexing. One bad recipe stops its own build instead of taking a whole collection out of every listing.
- `HasComponents` is exported for `internal/artifact/key.Role`. It decides whether an app or os dependency contributes to equivalence.
- Nothing lints a near-miss glued version such as `star2.7.11b`. A data dependency's name is often several components deep, and requiring all of it to reappear would false-positive on ordinary compact naming.

## Sources declared by a recipe

- `#SOURCE:` declares a file the build fetches: a name, an optional architecture, and either one URL or `ask:` and a prompt.
- A URL and a prompt both expand with the template, since a template's source URL is where it varies most.
- The name is all that survives expansion unchanged. The identity records the fetched digest against it.
- The architecture is `amd64` or `arm64`, Go's spelling, which artifacts and OCI indexes also use.
- It is read as a word directly after the name. A URL contains `://` and a prompt starts with `ask:`, so neither can be mistaken for one.
- Vendors spell architectures too many ways for a placeholder to stand in, so each architecture is a literal line.
- A name with lines only for other architectures is an error, not an unset `$CNT_SRC_<name>`.
- A name is all qualified or all plain. Mixing them would leave "which wins" to declaration order.
- `Recipe.Prompts` is the one ordered list of questions: the `#INPUT:` prompts, then each `ask:`.
- `#INPUT:` answers reach the recipe on stdin and `ask:` answers go to the fetch. Putting the recipe's own first tells them apart by count alone.
- A malformed `#SOURCE:` is skipped at parse and reported by `Validate`, so one bad recipe cannot hide a collection.
- At build time it is an error, not a skip. The body would otherwise run against an unset variable.
- A definition declares neither `#SOURCE:` nor `#INPUT:`. Nothing would fetch the source or read the answer.
- The header is a comment, so changing a `#SOURCE:` URL moves neither key. Only the bytes it served can, through the recorded digests.

## Decisions that may surprise you

- **Sorting happens once, where the separator is visible.**
  - `#PH:` value lists are ordered by their separator. Comma sorts newest first, pipe keeps the author's order.
  - `values[0]` is the default offered to users.
  - A directory source reads the source form and sorts.
  - An HTTP source reads the published index, which records no separator, so it takes the values verbatim. Re-sorting would silently reorder author-ordered lists.
  - The generator and this package are pinned to a shared table of cases, not to each other's source.
- **An unreachable source keeps its place.**
  - Dropping it would let first-wins promote the next source's recipes, and the build would look normal.
  - It stays in the list carrying its error. Lookups skip it, and the caller reports it once.
  - A catalog where every source failed is empty, not an error.
- **A stale index beats a failed fetch.** An expired cache entry with no route out is served with a flag set. That is a compute node, not an error.
- **The selected source owns its artifact endpoints.**
  - `source.json` may declare one OCI `push`, ordered `pull` endpoints and an `audience`.
  - A build keeps the exact source that won first-match lookup and tries only its endpoints.
  - A pulled artifact must match the equivalence key derived from that same recipe before it is installed.
  - An invalid descriptor disables its endpoints without hiding its recipes.
- **`>=` is for reuse, not for widening.**
  - `samtools/1.23.1>=1.10` admits `[1.10, 1.23.1]`. The preferred version is the implicit upper bound.
  - The lower bound only lets an installed artifact satisfy the dependency. It never reaches a fresh solve.
  - Handing micromamba a range would let two machines resolve one recipe to different versions.
- **Installed beats newer.** With no preferred version, the newest installed version in range wins, so a bare `#DEP:` does not rebuild the moment upstream moves.
- **Placeholder key order is not stored.** It is derivable from the `#TARGET:` token order, so storing it would be a second copy free to drift. Value order is stored, because it picks the default.
- **Expanding a template leaves `Text` alone.**
  - `Expand` sets `Rendered`, the substituted copy a build runs.
  - `Text` stays the template as fetched. It is what an artifact embeds and a rebuild starts from.
  - Every variant therefore shares one recipe digest, told apart by the placeholder values recorded beside it.
- **A template match is not a wildcard.** Names match the declared `#PH:` values. Only an explicit `*` is permissive.
- A half-filled name does not determine its remaining values, so there is no partial fill.

## Build dependencies

- `ValidateDeps` holds both rules a build's `#DEP:` answers to.
- Its callers are `Recipe.Validate` and `build.FromExternalSource`. Never `run` or `check`.
- It lives here, not on `Recipe`, because an external build (`create -p -f <script>`) answers to the same rules and has no recipe to parse.
- Only a `data` recipe may declare a dependency.
- An `app` is self-contained, and an `os` is self-contained by definition. Producing an index needs the tool that produces it, which is why data has dependencies.
- A build's `#DEP:` is a `name/version`, never a path. A running script's may be either.
- The asymmetry is the point. A running script mounts what it names and records nothing. A build's declaration becomes an edge in an artifact that must mean the same thing on another machine.
- A path is neither resolvable there nor a key anything can regenerate.
- `IsPathDep` is the single answer to "is this dependency a path", because this package owns the `Normalize` and `ParseDep` grammar.
- Its extension set must stay `utils.IsOverlay`'s, plus `.sif`.
- A `.sif` is a root-only, literal-path reference, never an overlay. It never stacks and carries no build identity.

## `#TARGET:` names the artifact

- Without a `#PH:` beside it, `#TARGET:` is not a template. It is how an external build gets a name.
- Without one, the name is the `-p` basename, a single component. `key.Role`'s component match never fires, and every `#DEP:` is silently downgraded to build history.
- `FromExternalSource` therefore refuses an external script that declares a dependency without one.
- The name and the file path are separate namespaces. `#TARGET:` fixes `/cnt/<name>` and the role classification. `-p` fixes where the `.sqf` lands.
- `create --name` beside `-p` sets the name the same way and wins over `#TARGET:`.
- With either, `-p` directly in an images directory must spell the name's own filename. With neither, the `-p` basename is the name, normalized first.
- A path-addressed artifact's filename therefore carries no naming claim. It is matched by manifest name alone.
- A flat or store scan still requires the filename to encode the name, because there the filename is the address.

## Invariants worth not breaking

- There is one spelling of a name. `Normalize` decides what a name is, and index keys use that form.
- A second implementation elsewhere would make one string resolve two ways. The same holds for `CompareVersions` and constraint satisfaction.
- The two backends must return identical entries for the same collection. A test builds one on disk, serves it over HTTP and diffs the results. That test is the contract.
- The caller owns the cache directory and TTL. The package owns everything inside it.
