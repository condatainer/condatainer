# internal/store

Immutable overflow below each images root. A flat overlay keeps the only bare-name slot. A store entry is addressed by artifact name plus a verified complete identity.

## Placement

- An exact flat or stored artifact is adopted first.
- Otherwise a free bare name is taken as flat, and the store is used only when some readable root holds that name at a different identity.
  - A flat artifact answers to `exec -o`, to `list` and to every other project.
  - Taking a free name keeps one identity from being rebuilt once per project.
- `store/` is not created unless a conflict sends something there.
- Occupancy is judged across every readable root, not the destination alone.
  - Reads are nearest-first and writes furthest-first, so a nearer flat copy would shadow a new install and leave two identities on one name.
  - The decision is re-checked under the target's producer lock. A writer that loses the name in between overflows instead of replacing it.
- `StoreOnly` skips the bare name even when it is free. `project restore` uses it for a dependency that shares a name with a selection.
  - It decides where a new artifact is written, not what answers to the name. An exact flat copy is still adopted.

## Locking

- Each target has a producer lock. There is no store-wide lock.
- Producer locks exist because a missing target has no inode to lock. Once the file exists, readers take a shared inode lock and removal takes an exclusive one.
- Publication is create-only. An exact target is adopted, and a conflicting target is never replaced.
- `Detach` hands a reserved target to a scheduler job by rewriting the lock with the job's identity.
  - The lock holds the pathname across the queue wait.
  - A job that never runs leaves a stale lock, which the next producer clears.

## Placing a file

- `InstallFile` names the destination from the keys inside the file, never from its filename. In the store the filename is the address.
- It copies. A hardlink would share mode bits and locks with the user's file and make `GC` count bytes a second link still holds. `Commit` refuses a symlink.
- A build always passes `StoreOnly`. It holds the producer lock on the flat target for its whole run, so trying flat placement would deadlock against itself.

## Promotion

- `Promote` only renames, and only inside the candidate's own root.
  - No identity leaves any reader's set, whatever their layers.
  - A project lock pins name and keys, not a path, so it still resolves both.
- The incumbent is demoted into that root's `store/` before the candidate takes the bare name, both under the bare name's producer lock.
  - A crash between them leaves both in `store/` with the name free. Re-running repairs it, and no `.part` sweep can eat it.
- A candidate in a root that a nearer root shadows is refused with `ErrShadowed`. Promoting it would change nothing that reads resolve.
- The artifact cache is not updated. Its fingerprint check misses a renamed path, and the cache is per-user so it could not reach other readers anyway.

## Removal and collection

- `Remove` handles store entries only. A flat artifact answers to a bare name and belongs to `remove`.
- `GC` judges and deletes in one traversal, so the lock taken to judge an entry is still held when it is unlinked.
- An entry is collectable when its exclusive lock is free, it is older than the grace, and it validated. Any uncertainty retains it.
- Age is `max(atime, mtime, ctime)`, and the report names which decided.
  - A refreshed atime from a backup or indexer only retains an entry.
  - A `noatime` mount leaves the other two to answer.
- The grace is `store_gc_grace` in days, default 30, overridable with `--grace`. It is per invocation, so `--apply` requires `--dir` or `--layer`.
- Abandoned `.part` files use a fixed 24-hour window. That is crash recovery, and it only asks whether a producer could still be writing.
- Reachability is never consulted. A manifest edge is not liveness, and no build dependency is ever installed, so every entry was asked for.
