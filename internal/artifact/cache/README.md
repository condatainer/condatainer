# internal/artifact/cache

A disposable per-user cache of what an image holds, so a command that scans many images does not open each one.

## What it is for

- Scans use it: `list`, `store list`, project resolve and pin, and the dashboard's overlay list.
- A launch never does, in a project or not.
  - It reads each image's runtime document itself.
  - Resolving a pinned name reads the keys of each same-name candidate from its image.

## Decisions

- Correctness never depends on it. A missing, stale or unreadable file only costs a read.
- A hit needs the file's inode, size, mtime and ctime to agree.
- The device number is not part of the fingerprint.
  - A network filesystem reports a different one for each mount.
  - The cache lives in the home directory, shared across nodes, so each node would miss the records of every other.
- A symlink is never cached.
- The process-wide cache records only files a scan reads: directly in an image root, or in its `store/` directory.
  - A file named with `-o`, a temp path and a directory a test passes in are never recorded.
  - Records for files that left a root are not removed. A scan does not prune, and a stale one costs about 8 us to load.
- A write that changes no record does not rewrite the file.
- Writers take a sibling `flock`, merge into the latest file and rename a temp file. Readers take no lock.
- A held lock never stalls a command.
  - A writer waits up to a second for it. The write is skipped after that, and the process stops trying.
  - The skipped records stay in memory for the process. Every record can be regenerated, so a later run fills them in.
  - A lock waited on forever would let one stuck writer on one node hold up every scan and project launch on the others.
- A scan persists once, through a batch.
- A record holds the runtime document and, for the store, the verified keys. They fill in independently.
- A file of another schema version is ignored, not migrated.
