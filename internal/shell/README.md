# internal/shell

## Why the setup is in the rc file

- `condatainer` writes it, not the install script. One command serves a hand install, a site's binary and a user who switches shell.
- The rc lines ask the binary for generated text. A fix reaches every user without editing their dotfiles.
- The hook line has no guard in the rc. `env hook` is the one check.
- The `shell` commands skip config initialization. They run from an rc file, before any config exists.

## What the setup owns

- The setup is a block that condatainer owns. Edits inside it are overwritten, and text outside it is never changed.
- A block is replaced where it is, so a user's later lines keep their order.
- A lone marker is left alone and a new block is appended, with a warning.
  - Refusing would leave the user to repair the file by hand.
  - Deleting from a lone start marker to the end of the file would destroy the user's own lines.
- fish gets a file that condatainer owns whole, so nothing of the user's is at risk.
- `remove` deletes that file's path itself. A symlink goes and its target stays.

## Which shells

- bash is set up with any other shell. A container starts bash whatever the host shell is.
- `remove` without a shell covers every shell. A user who changed shell may have left a setup in the old one's file.

## Files

- An rc file that is a symlink stays one. Replacing the link would detach a dotfiles repository.
- With `home_override` set, a container reads the replacement home's rc, so both are written.
- The user's login file is never edited. A login bash may skip the rc file, so `init` warns.

## The PATH line

- The directory is the running binary's, with symlinks resolved, so it is the real install.
- It is left out when `condatainer` on `PATH` already is this binary.
- The directory is the user's choice. `init` does not judge it.

## Containers

- `init` and `remove` refuse inside a container. The binary there is bound at a path that does not exist on the host.
