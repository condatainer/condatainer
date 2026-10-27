# Plan: remove builds from a registry

Status: implemented. To be deleted. `registry remove` and `registry gc` ran on a real GHCR package and a real
Docker Hub repository. `project registry gc` is tested against fakes and a real git repository,
and has not run on a real registry.

Builds on the tag layout in `registry-single-repo.md`. Every manifest has one build tag
`B__<id12>__<arch>`. The name tag `B__<arch>` points at the build a name serves, and the
equivalence tag `B__eq-<eq12>__<arch>` at the build the prebuilt check pulls.

## Goal

Three commands remove builds without a registry console. They follow the local `store`
commands:

| Local | Registry |
|---|---|
| `store remove <name>@<identity>` | `registry remove <name>@<identity>`, or `registry remove <name>` for every build of a name |
| `store gc` | `registry gc` |
| | `project registry gc` |

## Design

### Shared rules

- The unit is one manifest, which has exactly one build tag. A script build has one manifest
  per architecture under one identity. A Conda or frozen build can have a different identity
  per architecture.
- Every command lists the tags it moves and the manifests it removes, and asks. Unlike a push,
  the question defaults to no. `-y` skips it, as for `registry push`. There is no `--apply` and
  no `--dry-run`: answering no removes nothing.
- A run without a terminal and without `-y` is refused, so a script never removes by accident.
- `registry remove` and `registry gc` require `-s/--source <name>` and take no `--registry`. Only
  a configured source names a source repository, so neither can run against a project's.
- Neither checks which recipe collection a build came from. Only a local push does.
- A source's registry holds no frozen environment, since a push refuses one, so neither command
  treats one specially.
- The registry is read again after the answer, and only a manifest that still qualifies is
  removed. A push writes the build tag before the equivalence and name tags, so a fresh upload
  briefly looks untagged, and the user may take a while to answer.
- Tags are moved before any manifest is removed. A run that stops part way never leaves a tag
  pointing at a removed manifest.
- A removed manifest cannot be downloaded by a lock that pins it. The help says so.
  - Restore rebuilds a script, definition or Conda build from the lock's vendored recipe.
  - Only a frozen environment is lost for good, and a source's registry holds none.
- A tag is a name tag with one `__`, a build tag `B__<hex>__<arch>`, or an equivalence tag
  `B__eq-<hex>__<arch>`. `B` never contains `__`, so the spelling alone decides.
- A tag of any other spelling is never touched, and neither is the manifest it points at,
  since removing a GHCR version removes every tag on it.

### Remove a build or a whole name

`registry remove` takes one of two targets. `--arch` limits either to one architecture.

- `registry remove <name/version>@<identity>` removes that build on every architecture that
  has it.
- `registry remove <name/version>` removes every build of the name, on every architecture, with
  its name and equivalence tags.
  - Nothing is left of the name, so no tag moves.
  - It is how a version is retired. `registry gc <name>` keeps the builds the name and
    equivalence tags point at.
  - The list names each build, so the question shows what the name held.

For one build:

- The identity is a complete key or an unambiguous prefix, as `store remove` takes. A prefix
  that matches two builds is refused and names both.
- A name tag that points at the build moves to the newest other build of the name on that
  architecture. Newest is by `AnnCreated`, and a build with none counts as oldest. With no other
  build, the name tag goes with the manifest.
- An equivalence tag that points at the build moves to the newest other build of that class on
  that architecture. With no other build, it goes with the manifest.
- To publish a corrected copy of the same identity, remove it and push again. The build tag is
  gone, so the push uploads and writes every tag.
- It needs push permission to move tags and delete permission to remove.

### Collect untagged builds

- `registry gc [name]` removes every manifest that no name tag and no equivalence tag points
  at. Only its build tag is left on it.
- On GHCR it also removes a version with no tag at all, such as one left by an interrupted
  upload. No lock pins one, because every manifest a lock records had a build tag. Docker Hub
  does not list untagged manifests.
- A name argument limits the run to that name, and leaves out untagged versions, which carry no
  name.
- It moves no tag, because the manifests it removes carry no other.
- A Conda build has no equivalence tag, so it is collected once its name moves on.
- It needs delete permission.

### Collect a project repository

- `condatainer project registry gc` removes the manifests in the project's repository that its
  lock does not pin.
- The repository is the one `project registry set` recorded, so it takes no endpoint flag. The
  lock decides what is kept.
- Kept: every manifest the lock records as a remote in this repository, with its tags.
- It moves no tag. A name tag on a removed manifest goes with it.
- The project must pass `project validate`, or nothing is removed. A lock that does not match
  its checkout may pin less than the project needs.
- A repository a configured source declares is refused. It is a source's, and `registry gc`
  handles it.
- A lock at another git revision pins digests this one does not, so those manifests are
  removed and an older commit can no longer restore them. The help says so.
- `--git-history` also keeps every manifest a lock revision reachable from `HEAD` pins.
  - It reads `cnt-lock/lock.json` at each commit that changed it, with `git log` and `git show`.
    One small file per commit, so the cost follows the commits that touched the lock, not the
    size of the repository.
  - It sees only `HEAD`'s ancestry. A lock on another branch or an unpushed commit is not read,
    and the help says so.
  - A shallow clone is refused, since its history stops short and the run would remove what an
    older commit pins.
  - A frozen environment has no recipe to rebuild from, so removing one is final for an older
    commit. `--git-history` is the way to keep it.
- A repository holds one project. Collecting from one of two projects that share it would
  remove the other's builds.

### Removal

- Docker Hub and any registry that accepts the standard delete call use it. GHCR rejects it and
  uses GitHub's package API.
- A registry that answers `405 UNSUPPORTED` is reported as unable to remove, and the owner
  removes in its console.
- A push login usually lacks delete permission. The error names the permission to log in with.

## Implementation

### What the registries accept

Tested with `oras-go` against a scratch repository on each host.

| Call | Docker Hub | GHCR |
|---|---|---|
| `DELETE …/manifests/<tag>` | `202`. Removes that tag only. | `405 UNSUPPORTED` |
| `Repository.Delete` by digest | Removes the manifest and every tag on it. | `405 UNSUPPORTED` |

- `Repository.Tags` lists tags, and `Repository.Tag` adds or moves one. Both are used today.
- The tag list carries no digests. The standard backend resolves each tag to its digest, one
  request per tag.

### The removal backend

`internal/registry/delete.go` gives the commands one view of a repository, whatever the host.

```go
type Held struct {
    Desc ocispec.Descriptor // the digest, media type and size the standard delete needs
    Tags []string
}

type Backend interface {
    Manifests(ctx context.Context) ([]Held, error) // every manifest with its tags
    Remove(ctx context.Context, h Held) error      // the manifest and every tag on it
}

func BackendFor(base, repo string) Backend // ghcr.io is GHCR; any other host is standard
```

- The standard backend serves Docker Hub and any other registry.
  - `Manifests` lists the tags and resolves each to its digest, then groups them.
  - `Remove` calls `Repository.Delete` with the manifest's descriptor.
  - A `405` is returned as `ErrNoRemoval`, naming the host.
  - A 401 or 403 names the permission to log in with. On Docker Hub that is an access token
    with Read, Write and Delete.
- The GHCR backend uses GitHub's REST API on `api.github.com`.
  - `GET /users/<owner>` gives the owner's type, `User` or `Organization`. A user's packages
    are under `/users/<owner>`, an organization's under `/orgs/<owner>`.
  - The package name is the repository path after the owner, with `/` written `%2F`.
  - `GET <prefix>/packages/container/<package>/versions?per_page=100` lists the versions, paged.
    Each has `id`, the digest as `name`, and its tags in `metadata.container.tags`. One listing
    groups every tag, so it needs no request per tag.
  - `Remove` is `DELETE <prefix>/packages/container/<package>/versions/<id>`, which removes
    every tag on the version.
  - GHCR refuses to remove the last tagged version of a package, answering `400` with "You
    cannot delete the last tagged version of a package". That is `ErrLastVersion`. The error
    tells the owner to remove the package in GitHub's settings. The commands never delete a
    package, since that also drops its visibility settings and its repository link.
  - The `ghcr.io` credential, a login first and then `GITHUB_TOKEN`, goes to `api.github.com`
    and to no other host.
  - A 401 or 403 names the scope to log in with, `delete:packages`.
  - Checked against a scratch user package and a scratch organization package:
    - The owner lookup, the version listing and `ErrLastVersion` (user package only).
    - `Repository.Tag` moves a tag from one version to another. The old version keeps its other
      tags, and the listing shows the move within a second.
    - Deleting a tagged version while another tagged version remains, and deleting an untagged
      version, both answer `204`. A version left with no tag by a move is listed with no tags.
    - The organization's endpoints, `/orgs/<owner>/…`, behave the same as the user's.
- Both backends take their base URLs as fields, so tests point them at fakes.

### The plans

Each command builds a plan, shows it, asks, then applies it. `Publish` does the same with
`Confirm` and `ErrDeclined`.

```go
type TagMove struct {
    Tag                string
    FromBuild, ToBuild string // build tags
    To                 ocispec.Descriptor
}
type RemovePlan struct {
    Moves   []TagMove
    Removes []Held
    Kept    []Held // matched, but hold a tag this package did not write
}
type RemoveTarget struct{ Name, Identity, Arch string }

func PlanRemove(ctx, base, repo string, b Backend, t RemoveTarget) (RemovePlan, error)
func PlanGC(ctx, b Backend, name string) (RemovePlan, error)
func PlanUnpinned(ctx, b Backend, pinned map[string]bool) (RemovePlan, error)
func Apply(ctx, base, repo string, b Backend, p RemovePlan) (ApplyResult, error)
```

- `Apply` runs in this order, and stops at the first failure:
  1. Make every move with `Repository.Tag`, and resolve each moved tag to check it.
  2. Read `Backend.Manifests` again. Skip any planned manifest that now holds a name or
     equivalence tag, or a tag it did not hold when planned.
  3. Remove what is left.
- `cmd` prints the plan and asks `Remove? [y/N]`. `-y` skips the question.

```
Move 2 tags
  star--2.7.11b__amd64               9f2c1ab4d3e8 → 7a31c0de92b4
  star--2.7.11b__eq-e90d3b7714cf__amd64   9f2c1ab4d3e8 → 7a31c0de92b4
Remove 1 manifest
  star--2.7.11b__9f2c1ab4d3e8__amd64
```

### `registry remove`

A whole name skips steps 3 and 4: it removes every manifest holding a tag of that name and
architecture, and moves nothing.

1. Build the build tag for each wanted architecture with `Address.tag`, so a short identity is
   matched against the tag list. An architecture with no such build is skipped. None found is
   `ErrNotFound`, and `PublishedArches` says what exists.
2. Resolve each build tag to its digest, and find that digest in `Backend.Manifests`.
3. For each name or equivalence tag the manifest holds, list the other build tags of that name
   and architecture and fetch each one's annotations.
   - A name tag moves to the newest of them.
   - An equivalence tag moves to the newest whose `AnnEquivSHA` matches.
   - Newest is by `AnnCreated`. A missing one counts as oldest, and a tie goes to the larger
     build tag, so the choice is the same on every run.
4. Add the manifest to `Removes`. A tag with no other build to move to goes with it.

### `registry gc`

1. `Backend.Manifests` once.
2. Sort each manifest's tags by spelling: name, build, equivalence, other.
3. Collect a manifest that has a build tag and no name, equivalence or other tag. On GHCR also
   collect one with no tag at all, unless a name argument is given.
4. A name argument keeps only manifests whose build tag encodes that name.
5. With `--no-recipe`, a manifest also qualifies when its name has no recipe in the source,
   whether or not a name or equivalence tag points at it.
   - The source's recipe list is refreshed first, since a cached list would call a new recipe
     missing.
   - An unreadable, stale or empty list stops the run.
6. After the answer, read `Backend.Manifests` again and remove only the manifests that still
   qualify.

### `project registry gc`

1. Load the lock and run the same check as `project validate`. Refuse an invalid project.
2. Read the repository from the project's registry setting. Refuse one that a configured
   source declares.
3. Pinned digests are the lock's remotes in that repository. With `--git-history`:
   - Refuse a shallow clone, found with `git rev-parse --is-shallow-repository`.
   - List the commits that changed the lock with `git log --format=%H -- cnt-lock/lock.json`.
   - Read the lock at each with `git show <commit>:cnt-lock/lock.json` and add its remotes.
   - A revision whose lock does not parse stops the run and names the commit.
4. Refuse an empty pin set, which would remove the whole repository.
5. `Backend.Manifests`, then remove every manifest not pinned. After the answer, read it again
   and remove only those still not pinned.

## Limits

- Deleting a manifest by digest drops every tag on it. GHCR cannot remove one tag of a
  version either. So a tag is always moved off a manifest before the manifest is removed.
- GHCR refuses the standard delete calls, so only GHCR needs a backend of its own.
- On GHCR, removing a version removes every tag on it, so a name or equivalence tag with no
  build to move to goes with the version.
- Grouping tags by manifest takes one request per tag on the standard backend, and one paged
  listing on GHCR.
- `remove` fetches one manifest for each other build of the name on that architecture, for its
  `AnnCreated` and equivalence key. Both come from the same fetch.
  - It fetches only when the removed build holds the name or an equivalence tag.
  - On Docker Hub a manifest fetch counts as a pull, and a tag resolve does not.
  - A `429` waits for its `Retry-After`.

## Files

| File | Change |
|---|---|
| `internal/registry/remove.go` (new) | find a build's manifests by identity, move its name and equivalence tags, remove it |
| `internal/registry/gc.go` (new) | `PlanGC`: manifests with only a build tag, and untagged GHCR versions. `PlanUnpinned`: everything a pin set does not hold |
| `internal/registry/delete.go` (new) | `Backend`, `Held` and `BackendFor`; the standard backend and the GHCR backend, shared by every command |
| `internal/registry/plan.go` (new) | `RemovePlan`, `TagMove`, and `Apply` |
| `internal/registry/delete_test.go` (new) | the standard backend against `fakeRegistry`, and the GHCR backend against an httptest fake of the packages API |
| `internal/project/publish/gc.go` (new) | `Pinned` reads the lock's remotes in the project repository, and `HistoricPinned` those of earlier commits |
| `cmd/registry.go` | `remove` and `gc` with a required `-s/--source`, `--arch` (remove only) and `-y` |
| `cmd/project.go` | `project registry gc` with `--git-history` and `-y`; the registry group's help lists it |
| `internal/registry/README.md` | record why GHCR has its own backend, why `remove` and `gc` take only `--source`, why tags move before a manifest is removed, and why the registry is read again before removal |

## Tests

- One table test of `remove`: a name tag and an equivalence tag moved to the newest other
  build, each removed with the manifest when no other build exists, `--arch` limiting to one
  architecture, an ambiguous prefix refused.
- One test that `remove <name>` removes every build, name tag and equivalence tag of that name
  and leaves another name's builds alone.
- One test that `remove` moves every tag before it removes a manifest, and removes nothing
  when a move fails.
- One test per backend that `Manifests` groups tags by digest and `Remove` calls the host's
  delete, against the fakes. The GHCR test covers paging, and a 403 naming the scope. The
  standard test covers a `405` becoming `ErrNoRemoval`, and the GHCR test a `400` for the last
  tagged version becoming `ErrLastVersion`.
- One test that a manifest which gains a name tag between the plan and the answer is kept.
- One table test of `gc`: a manifest with only a build tag removed, one with a name tag or an
  equivalence tag kept, a manifest with a tag of unknown spelling kept, an untagged GHCR version
  removed without a name argument and kept with one.
- One test that `remove` and `gc` require `--source`, and refuse a registry with no removal
  backend and a run without a terminal and without `-y`.
- One test that `project registry gc` keeps every manifest the lock pins and removes the rest,
  and refuses an invalid project and a repository a source declares.
- One test that `--git-history` also keeps what an earlier commit's lock pinned, and refuses a
  shallow clone.

## Verification

- On a scratch GHCR package and a scratch Docker Hub repository: push two builds of one name on
  two architectures, `registry remove` the current one, and check that the name and
  equivalence tags serve the older build. Push the removed build again and check it uploads.
- Push a third build so the first has only its build tag, run `registry gc`, and check that only
  that manifest goes. Confirm the permission each registry needs is the one the error names.
- Push a project twice with a changed pin, run `project registry gc`, and restore the project
  from the collected repository on a clean machine.
- `go test ./...`, `gofmt -l .`.
