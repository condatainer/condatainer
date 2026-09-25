# internal/artifact

What CondaTainer embeds in an image: what it says it is, what loading it does to the environment, and how two images are compared.

- It reads through `internal/image`, which knows nothing about metadata. The dependency is one way.
- Both embedded documents are trusted, not verified. Validation checks structure, not truth.

## Two documents

- `runtime.json` is read on every mount. `manifest.json` is read on demand.
- The split is the point. One document would make every provenance field cost every `exec`.
- `runtime.json` stays small and stops growing. The manifest is free to grow.

### runtime.json

- It holds only what container setup needs.
- `Prefix` is where the payload sits in the archive, not a mount point. Readers take it as recorded.
- `Env` keeps `{prefix}` and is substituted at load time, since the prefix is unknown at build time.
  - An `os` artifact needs no special case. Its prefix is empty, so `{prefix}/bin` is `/bin`.
- `ReadRuntime` tells "no runtime document" from "could not look".
  - Only a truly absent one is `ErrNoRuntime`.
  - A missing `unsquashfs` or a corrupt archive is never reported as "no metadata".
- There is no fallback to the manifest for an older image.
  - It mounts, contributes nothing, and reports the same as an image with no metadata.
  - A fallback would add a second archive read and two code paths that must agree on what a runtime is.
- A missing or unreadable runtime never blocks a mount.
- Reads are cached across processes, keyed by path and validated by size and mtime.
  - `list`, `avail`, PATH construction and shell completion would otherwise spawn one `unsquashfs` per image each time.
  - Negative verdicts are cached too, or an old image is re-probed on every listing.

### manifest.json

- It records what the image is and where it came from. It carries no runtime block.
- `type` and `build_type` are separate and neither derives from the other.
  - `type` is what the payload is. `build_type` is how it was produced.
  - One recipe language produces both an app and a data image.
  - The one overlap is a frozen environment: `snapshot` and `env` imply each other, and `ValidateManifest` refuses a mismatch.
- Both are closed enumerations. An unknown value is refused, never mapped to something safe.
  - The type decides the prefix, the runtime rules, the tag shape and whether a public endpoint accepts it.
  - Reading an unknown type as `app` would answer all of those confidently and wrongly.
  - `Normalize` only fills an absent `type` as `app` and an absent `os` as `linux`. It never rewrites a wrong value.
  - The cost is that an older build cannot read a newer type. Silence and a wrong answer must not look the same.
- `dependencies[].identity` and `.equiv` are complete keys, scheme and SHA-256. A bare digest is a weaker edge than the artifact it points at.
- Everything in `build` is diagnostic. No scheme hashes it, with one exception: `build.from.digest` reaches `definition-identity-v1`.
  - `build.source` and `build.created` must never move a key. The same recipe built from a mirror, or twice, is one artifact.
- Manifest reads are uncached, since nothing asks for one per `exec`.

## Keys

- Each build type has a fixed pair of schemes: identity and equivalence.
  - script: `script-identity-v1`, `script-equiv-v1`
  - def: `definition-identity-v1`, `definition-equiv-v1`
  - conda: `conda-explicit-v1`, `conda-environment-v1`
  - snapshot: `payload-tree-v1` for both
- A key is the pair (scheme, SHA-256). Equal digests under different schemes are different keys.
- The dispatcher only selects the pair. Each scheme file states its own question, inputs and exclusions.
- Verification reruns the named implementations and compares digests. An unknown scheme, a mismatched pair or a key without a scheme is an error.
- A snapshot's two keys are one value. It has no inputs to abstract away, so "same environment" and "can substitute" cannot come apart.
  - Its preimage is the packed payload, so verification takes the recorded keys as they stand.
- Conda schemes hash the stored exports byte for byte and skip the canonical model.

### Script schemes

- Identity pins every direct dependency by name and exact identity.
- Equivalence contributes only what substitution depends on.
  - A data dependency contributes its equivalence key.
  - An app or OS named by the artifact contributes name and version.
  - A history-only app or OS contributes nothing.
  - Only a data dependency's equivalence key is read, so a build can derive its key before that dependency exists.
- The manifest freezes each dependency's role, so verification never reapplies a newer policy.
- A dependency missing a key is unrecorded, and `provenance_complete` becomes false.
- `#SOURCE:` files enter identity as `name=digest` of the bytes as served.
  - The URL never enters. A location is not part of what the build was.
  - The manifest records `{name, sha256}` only, so a link carrying an auth token is never published.
  - Equivalence carries no sources. A re-cut file makes a different build that still substitutes for what a recipe asks.

### The payload key

- `keys.payload` is `payload-tree-v1` over what the archive holds. It is independent of the other two.
  - Identity comes from the build record. The payload key comes from the files.
- Ownership and times are left out. The archive flattens ownership, and touching a file changes nothing.
- The metadata directory is left out, so the manifest carrying the key can live in the archive it describes.
- Only a recipe's build records one.
  - A Conda environment is pinned by its export, and a definition by its upstream digest.
  - Keying an OS rootfs is the slowest walk there is.
- A build hashes the directory `mksquashfs` is about to read. An installed `.sqf` is hashed through a mount with the same `_payload_key` walk.
- It is not a security measure. Whoever forges an archive computes the key of their own tree.

## Capsule

- `/.cnt/provenance` is the source closure an artifact was built from, one flat directory per artifact.
- Name and identity together address an entry. One solve published under two names has one identity.
- `runtime.json` and payload bytes are never copied. Entries exist to verify and rebuild.
- It is composed, not traversed: a capsule is the union of each direct dependency's sources and that dependency's own capsule.
  - Completeness is inductive. If every dependency's capsule is complete, the union is.
  - Cycles cannot occur, since a dependency image existed before the artifact that mounts it.
  - `Validate` still rejects a self-reference. A capsule read from elsewhere is not trusted.
  - There is no catalog access and no network at build time.
- `provenance_complete` is inherited. One unrecorded dependency below makes everything above it incomplete.
- The closure is shallow. Only data has dependencies, so it is a chain through data with apps and OS as leaves.

## Comparison

- `compare` is the one place that decides whether a candidate is the artifact that was asked for.
- `Read` recomputes every key from the file `manifest.keys` names. A key that disagrees with its own file is what verification catches.
- Gates run first, then keys.
  - Architecture, against the host and not the other artifact. `noarch` passes anywhere.
  - Type. An app and a dataset are never interchangeable.
  - Name. A substitution question is about a requested name.
  - Usability. An image whose records cannot be read makes no claim.
  - Runtime. `runtime.json`'s `env` must equal the identity record's `env=` lines.
    - `runtime.json` was never hashed, so an edited one cannot agree with the record.
    - `prefix` is not checked. The image states where its payload sits.
- The verdicts are `exact`, `equivalent`, `different` and `unverifiable`.
  - `unverifiable` is not `different`. It means the question was not answered.
  - A gate failure is a verdict with a specific diff, never a fall-through. A key match between non-comparable artifacts would mean nothing.
- Diffs read `manifest.dependencies`, since the equivalence record drops a dependency's name.
  - A history-only dependency change shows in a diff while the verdict stays `equivalent`. That asymmetry is intended.
- `MountAllowed` is the one comparison on the execution path.
  - `runtime.json` is already in hand and holds `platform`, so it costs a string comparison.
  - Without it a wrong-architecture image mounts cleanly and fails somewhere the cause is unrecognizable.
- Records prove nothing about the payload. Two images with identical records and different payloads are easy to make.
  - An untrusted source needs a signature policy, which this does not provide.
