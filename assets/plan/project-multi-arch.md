# Plan: a project lock that holds one build per architecture

Status: implemented. To be deleted. Run on one architecture only; a second architecture's lock, push and
restore are covered by tests and not yet run on real hardware.

Builds on the tag layout in `registry-single-repo.md`. Every manifest has a build tag
`B__<id12>__<arch>`, and the architectures of one artifact have disjoint tags.

## Goal

Fix the lock format for more than one architecture before release. A project that uses one
architecture today needs no format change to add a second.

## Current state

- A pin maps a request to one artifact directory, `provenance/<name>-<identity digest>`.
- `runsOn` (`internal/project/restore/plan.go`) refuses an entry whose architecture is neither
  `noarch` nor the host's.
- `project lock` on a second architecture pins that machine's images under the same request
  keys and replaces the first architecture's pins.
- A Conda build's identity is its `explicit.txt`, which differs per architecture.
- A script identity pins every direct dependency by exact identity, whatever its role. A
  history-only dependency is in identity and out of equivalence. Identity differs per
  architecture as soon as one dependency does. It is shared only when every dependency has
  one identity everywhere or the script has none.
- A recipe's `#ARCH:` is `noarch`, or the list of architectures it builds on, and absent
  means any. The built overlay records the one architecture it was built on.
- `noarch` says the payload runs on any host. It does not make the identity the same: a
  `noarch` data overlay built over an architecture-specific dependency has one identity per
  architecture.
- A def build's identity differs per architecture when its root does.
- A frozen environment's identity is its payload, which exists for the architecture it was
  captured on.
- The `base:` pin is an `os` overlay, so it differs per architecture too.

## Design

### Format

- A pin holds one artifact per key: `amd64`, `arm64` or `noarch`.
  - An overlay built for one architecture is keyed by the architecture of the host that
    locked it.
  - A `noarch` overlay is keyed `noarch`, with one entry, wherever it was built.

  ```json
  "samtools/1.20":  { "artifacts": { "amd64": "provenance/samtools-1.20-ab12…",
                                     "arm64": "provenance/samtools-1.20-cd34…" } },
  "hg38-index/1.0": { "artifacts": { "noarch": "provenance/hg38-index-1.0-b7e2…" } }
  ```

- The lock gains `archs`, the sorted architectures it covers, such as `["amd64", "arm64"]`.
  It is declared, not derived: an empty, `noarch`-only or half-locked lock gives nothing to
  derive it from.
- An entry's build history is not stored. It is the set of non-`noarch` architectures among
  its closure manifests, and empty for an overlay with no architecture-bound history.
- The request stays architecture-neutral. `Manual` stays on the pin, not on an architecture.
- Entry directories stay flat under `provenance/`, named by identity. There is no
  per-architecture directory.
  - Two architectures' entries never collide, because their identities differ.
  - A `noarch` entry with one identity everywhere is stored once, because its manifest records
    `noarch` on every host.
  - The directory stays the one an image carries at `/.cnt/provenance/`, read by the same code.
- A shared identity of an overlay built for one architecture has no separate place.
  - The entry name is name plus identity, and the manifest in it records the build's
    architecture, so the two architectures would write one directory with different bytes.
  - `project lock` refuses it: the entry exists with another architecture. The message says to
    declare the recipe `noarch` if it is portable.
  - A recipe declares portability. Nothing infers it.
  - The entry name does not gain an architecture. It is computed in many places, and a
    portable recipe has a way out.
- Each architecture's closure is vendored beside the other's. A dependency's identity is part
  of its dependent's identity, so a closure is per architecture as soon as one leaf is.
- `Remote` is unchanged. Remotes are keyed by entry path, and an entry has one architecture or
  is `noarch`, so the path already says which digest is which.

### `noarch` overlays

- A `noarch` pin has one entry and one remote. The payload is portable, so the recorded
  digest is fetched on every architecture, and an adopted copy works anywhere.
- Its tags end in `__noarch`. A build made on another architecture has its own build tag, and
  shares the name and equivalence tags with the others. The lock names one build by identity.
- A rebuild runs the closure's tools, so it works only where they run. On another
  architecture restore refuses, names the pin, and says the registry copy is the way to get it.
- `--no-prebuilt` is therefore not possible for it on another architecture. Reproducibility
  there depends on the registry copy.
- An overlay with no architecture-bound history rebuilds anywhere.

### Behavior

- The lock is written by one architecture at a time. A project that uses one architecture
  has one key in every pin.
- `project lock` adds the host architecture to `archs` and pins every request for it in one
  run. It refreshes only the host's artifacts and leaves the others.
- A pin whose recipe's `#ARCH:` list leaves out the host cannot be pinned there. `project lock`
  reports it and still adds the architecture.
- `project validate` is checkout-local and runs on any host. For every architecture in `archs`,
  every pin must have that architecture's key or a `noarch` one, unless its recipe's `#ARCH:`
  list leaves that architecture out. The recipe is in the vendored sources, so this needs
  nothing stored. A miss names the pin and the architecture to run `project lock` on.
- A host whose architecture is not in `archs` gets one message, not one per pin. It says to run
  `project lock` there to add the architecture.
- Reconcile sweeps per request. A request the scan no longer produces drops every architecture.
- Restore takes the host's artifact, then `noarch`. With neither, it refuses and names the pin
  and the architecture to run `project lock` on. For a pin whose recipe leaves the host out,
  it says the recipe builds on the listed architectures only.
- Restore never rebuilds from another architecture's recipe.
- `project registry push` appends the host's remote. `project registry gc` keeps all of them.
- Verification checks every architecture's entry, whatever the host.
- Locking from a second architecture needs nothing more. Its artifacts are added beside the
  first's.

### Frozen environments

- A frozen environment has one architecture. There is no recipe to rebuild it from.
- Restore on another architecture reports it as captured on one architecture and names the pin.

### Matching

- `match identity` and `match equivalence` apply per architecture, against the host's artifact.
- Rebuilding on one architecture from another's recipe is out of scope. It would need an
  equivalence-only restore, and a Conda `explicit.txt` cannot be replayed across architectures.

## Files

| File | Change |
|---|---|
| `internal/project/lock/schema.go` | `PinEntry.Artifacts` keyed by architecture; `Lock.Archs`; validate both against the three architecture values; `PinnedArtifacts` walks every architecture |
| `internal/project/lock/pin.go` | `applyEntry`, `MatchPin`, `Reconcile` and the base pin write and sweep the host architecture only; refuse an entry that exists with another architecture and is not `noarch`; report a pin whose recipe leaves the host out |
| `internal/project/lock/verify.go` | verify every architecture's entry |
| `internal/project/restore/plan.go`, `restore.go` | pick the host's artifact, then `noarch`; refuse naming the pin, and for a rebuild whose closure cannot run here, name the pin and the registry copy; `runsOn` unchanged |
| `internal/project/publish/plan.go` | push the host's artifacts; record each remote under its entry path |
| `internal/project/standing.go`, `resolve.go` | read the host architecture's artifact, including `base:` |
| `cmd/project.go` | `project list`, `unpin` and `validate` report per architecture |
| `internal/project/README.md` | a pin is one artifact per architecture; why identity forces it |

## Tests

- One test that a second architecture's `project lock` adds its artifact and leaves the first's.
- One test that `project validate` fails when a pin lacks the artifact for an architecture in
  `archs`, and gives one message on a host outside `archs`.
- One test that restore picks the host's artifact, then `noarch`, and refuses with the pin
  named when it has neither.
- One test that `project validate` does not ask for an architecture's key on a pin whose recipe
  lists other architectures only, and that `project lock` reports such a pin on the host.
- One test that a `noarch` pin with architecture-bound history is refused for a rebuild on
  another architecture, naming the pin, and is fetched from its recorded remote.
- One test that `project lock` refuses an entry that exists with another architecture and is
  not `noarch`, and pins a `noarch` entry once under `noarch`.
- One test that a push from a second architecture records its remote under its own entry and
  leaves the first's.
- One test that Reconcile drops every architecture of a request the scan no longer produces.
- One test that `project registry gc` keeps every architecture's remotes.

## Verification

- Lock and push a project on amd64, then lock and push it from arm64. Restore on a clean machine
  of each architecture and check that each fetched its own manifest.
- Restore on arm64 from an amd64-only lock: the refusal names the pin.
