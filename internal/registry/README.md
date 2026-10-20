# internal/registry

Publishes and fetches immutable artifacts through an OCI registry.

## What a public endpoint takes

- `#REDISTRIBUTE:` outranks every type default.
- The defaults only restate what the author asserted by choosing a `#TYPE:`. They never guess licensing.
- Nothing overrides a refusal: no flag, no force.
- The pusher is rarely the party who agreed to the vendor's terms.
- A public push cannot be taken back.

A Conda build and a frozen environment skip the app default.

- Neither embeds a recipe, so `#REDISTRIBUTE:` has nowhere to travel with the artifact.
- Applying the default would be a permanent refusal.
- Channels a tool cannot judge (private, vendor) are reported at push time. A person decides.

These checks are deliberately absent. A tool that guesses permissive cannot take it back.

- **Adjudicating a Conda build's channels.** An allowlist cannot be kept honest, and licence strings need interpreting.
- **Deriving permission from `#LICENSE:`.** It is an SPDX expression, published verbatim and never parsed.
- **Letting config list permitted types.** `types: [app]` on a public endpoint would silently erase the rule.

`audience` is a declaration about the registry.

- Nothing verifies it.
- It is unrelated to a GitHub package's own visibility, which CondaTainer never reads or changes.

## Registry limits

GHCR is the registry these come from.

- **Layer size: 10 GB, documented.** Layers are cut below it.
- **Upload time: 10 minutes, documented.** A layer that cannot finish inside it is refused up front.
- An upload timeout is not a rate limit, so nothing retries it. Starting the layer would only waste the bandwidth.
- **Token lifetime.** ORAS fixes the credential when an upload session opens. A layer that outlives it cannot be saved.
- **Requests: unpublished.** GHCR applies a secondary rate limit on the number of requests.
- A 41-layer push was refused at about its 101st request.
- The limit shows up as a `429`, or as a `403` with GitHub's secondary-limit message.
- It applies to pushes and to pulls.
- **Manifest: 4 MB, accepted everywhere.** It bounds the layer count in theory and never binds in practice.

The request limit shapes the design.

- Layers are planned by count, not by size. A bigger artifact gets bigger layers, not more of them.
- Writes are spaced out.
- A rate limit is waited out, never treated as a failure.

Only documented limits are recorded per registry. An undocumented guess becomes stale policy.

## Rate limits and fallback

- A caller falls back to a local build only when an artifact is unavailable.
- A rate limit is not unavailable. Waiting is enough, and falling back would turn a wait into a rebuild.
- A throttled pull therefore waits. It never falls back.
- A rate limit is recognized only on positive evidence, never guessed.

## Identity and digest

Identity is defined in `internal/artifact`.

- A digest is the OCI content address. It names exact bytes and survives mirroring.
- An identity says what the artifact is. It comes from the artifact itself, never from the caller.
- A mirror cannot claim an artifact by asserting metadata about it.
- A manifest's architecture is the one the artifact records, not the pushing machine's. One machine can publish every architecture's build.

## Credentials

Storage and lookup are in `internal/credential`. What is particular to registries:

- Two kinds reach a registry: a login, from `registry login`, and a source's registry token, stored with the source by `config source add`.
- A source token belongs to its source, so removing the source removes it, and nothing a source does touches a login.
- The order depends on what the request is for:
  - A build pulling from a source's registry tries that source's token first. It was made for exactly that registry.
  - Any other read tries the login first, then source tokens in search order.
  - A push tries the login first. A source token is normally read-only, and a personal write token is what a publisher logs in with.
- `GITHUB_TOKEN` applies to `ghcr.io` only, after the stored credentials, except a push's source tokens.
- Late, because a person often sets it for other tools, and early would silently replace the group's credential. CI has no stored credential, so CI still uses it.
- A refused read moves to the next credential, then to none. A stale or wrong-account credential must not fail a read another one, or none, can do.
- One warning names each refused credential once a later one answers. When all are refused, the error names them all.
- Only a 401 moves on. A rate limit can arrive as a 403 and must be waited out, not retried.
- A push never moves on. It names the credential refused and the key to log in to.

## Tags and layout

- A source or a project keeps every artifact in one repository, and the name goes in the tag.
  - A GHCR package has one visibility, so a lab sets it once.
  - Docker Hub allows `user/repo` only, so a name with a slash cannot be a repository path.
- A tag is `<name>__<arch>`, `<name>__<id12>__<arch>` or `<name>__eq-<eq12>__<arch>`.
  - The name is the encoded artifact name. `--` stands for `/`.
  - `<arch>` is `amd64`, `arm64` or `noarch`.
  - `id12` and `eq12` are the first 12 hex characters of the identity and equivalence digests.
- A name segment never holds a doubled separator, so `--` and `__` cannot occur inside a name and a qualifier is never part of one.
- A tag is refused when it does not fit 128 characters. It is never truncated, since a truncated tag is another artifact's address.
- A tag carries a key prefix, so the full key in the annotations is compared on every lookup.
- The architecture is in the tag and in `org.condatainer.arch`. The annotation outranks the tag.

## No image index

- Every architecture is its own manifest, and no index is written.
- An index loses its tag whenever an architecture is added or a name moves. The untagged version it leaves can be deleted by a registry's cleanup, and a lock may pin its children.
- Merging into a shared index also folds another build's architectures into this one.
- The architecture comes from the tag, so a pull needs no index.
- A tool that expects one multi-arch tag does not work against these repositories.
- Base images such as `ubuntu:24.04` are other people's, so their indexes are still read.

## Which tags a push writes

- Order is build tag, equivalence tag, name tag. A push that stops part way leaves a tagged manifest.
- The build tag is the retention anchor. A lock pins a manifest digest, and a registry cleans up untagged versions.
- The build tag is immutable. Publishing a build whose tag exists uploads nothing and writes the missing tags.
- The name tag moves to every published build. The build it leaves keeps its own tag.
- The equivalence tag is for the prebuilt check, which asks for it directly so the name tag's position does not matter.
- Only script and def builds get one. A Conda build has no prebuilt to match, and a frozen environment's equivalence key is its identity.

## Sources and collections

- A push to a source's repository warns when the artifact records another collection as `org.opencontainers.image.source`, and asks before going on.
  - That keeps a source's repository from mixing collections by accident.
- Nothing on the read side checks a collection.
  - Published artifacts are read-only, so a renamed collection would leave every old one failing the check.
  - The keys already prove an artifact can stand in for a recipe.
- A source declares its own audience. `--audience` applies to `--registry` only, so a flag never loosens a source's.
- A project repository is reached with `--registry`. No collection is checked there.
