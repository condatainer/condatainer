# internal/utils

## File lock

- `AcquireFileLock` is an `fcntl` lock, not `flock`, because Apptainer uses `fcntl` on a mounted ext3 image.
  - `flock` conflicts with an `fcntl` lock only on filesystems that fold the two together. NFSv3 does, NFSv4 does not.
- A running `.img` is left to Apptainer, which locks it itself. Only `.sqf` and `.sif` are locked here.
- A write lock opens `O_RDWR`, because an exclusive `fcntl` lock needs a writable descriptor.
  - An image with its write bit cleared is therefore protected.
- A shared lock opens `O_RDONLY`, so a protected image can still be read.
