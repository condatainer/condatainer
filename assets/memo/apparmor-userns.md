# Memo: AppArmor, user namespaces and nested containers

On Ubuntu 24.04 and later, a user namespace made by an unconfined program is refused. Every CondaTainer step that needs a namespace fails on such a host, except where an allowed apptainer is already around it.

This memo holds what was measured.

## Symptom

- `condatainer overlay freeze env.img` on the host failed: `unshare: write failed /proc/self/uid_map: Operation not permitted`, then `pack ...: exit status 1`.
- The libexec apptainer failed on every launch: `ERROR  : Could not write info to setgroups: Permission denied`, then `Error while waiting event for user namespace mappings: no event received`.
- With `--fakeroot` or `build` it printed `Failed to create mount namespace: mount namespace requires privileges, check Apptainer installation`.
- With `--fusemount` it printed `Failed to create container process: Operation not permitted`.
- The apptainer from the Apptainer PPA worked on the same host with no change.
- The same freeze, run inside a container started by either apptainer, worked.

## Cause

- `kernel.apparmor_restrict_unprivileged_userns = 1` is the default from Ubuntu 24.04. It is set in `/usr/lib/sysctl.d/10-apparmor.conf`.
- An unconfined program that creates a user namespace is moved into the `unprivileged_userns` profile. The kernel log says `Userns create - transitioning profile`.
- That profile denies capabilities. The next log line is `DENIED operation="capable" ... capname="sys_admin"`, so the uid map and every mount fail.
- A program is allowed when its own profile has the rule `userns,`. Profiles attach by program path, and only root installs them.
- Without a profile, the denied program is the starter: the log names `starter` and the libexec path.
- A plain `unshare` is denied the same way, and no profile covers it.
- The PPA package installs `/etc/apparmor.d/apptainer`. It is loaded as `apptainer` and attaches to `/usr/bin/apptainer` and `/usr/libexec/apptainer/bin/starter{,-suid}`. That is why the PPA apptainer needs no step.
- The libexec apptainer has no profile, so it is refused until an administrator adds one for its path.

## Tested on

- An Ubuntu 26.04.1 VM, kernel 7.0, with the restriction on. Two apptainers, both 1.5.5 and both non-setuid:
  - The PPA package `apptainer`.
  - The libexec bundle, with and without a profile of its own.
- The development host, nested, in a helper container. It is RHEL 10 with no AppArmor, and the libexec apptainer there is 1.5.4. It shows what works nested, not what AppArmor does.
- Not tested: Ubuntu 24.04, a setuid apptainer, and other distributions with a namespace limit.

## The profile

- A working profile for libexec is the package's profile with the libexec paths: `<libexec>/bin/apptainer` and `<libexec>/lib/apptainer/bin/starter`.
- It has `flags=(unconfined)`, the rule `userns,` and `include if exists <local/...>`.
- A profile with `userns,` and no `flags=(unconfined)` fails on every case, before any namespace is made.
  - The program is then confined, and the log shows `DENIED` on `open` of `/etc/ld.so.cache` and of the shared libraries.
  - Even `apptainer --version` fails: `error while loading shared libraries: libpthread.so.0: cannot open shared object file: Permission denied`.
  - So the flag is required. A confining profile would list every file the starter and its helpers touch.
- A process inside the container runs under the starter's profile, as `condatainer-apptainer (unconfined)` or `apptainer (unconfined)`.
- Because of that, `unshare -r`, `unshare -r --mount` with a tmpfs mount, `unshare -r mknod` and `unshare -r mke2fs -d` all work inside the container.
- `unshare` run on the login shell fails in the same session. The permission follows the apptainer's process tree, not the user.
- A profile grants the permission to everything that runs under that apptainer. It is the effect of the sysctl set to 0, for one program tree.

## Results on the VM

| Case | PPA apptainer | libexec, no profile | libexec, profile |
|---|---|---|---|
| `exec` | pass | fail | pass |
| `--overlay` of `.sqf` and writable `.img` | pass | fail | pass |
| `--fusemount` of `squashfuse_ll` | pass | fail | pass |
| `exec --fakeroot` | pass | fail | pass |
| `build --sandbox --fakeroot` | pass | fail | pass |
| `unshare` inside | pass | fail | pass |
| `overlay freeze` inside | pass | not run | pass |
| the same apptainer inside, started by a shell | not applicable | fail | pass |

- The stacked freeze job passed all 12 checks with the PPA apptainer and with the libexec apptainer under its profile.
- That job ran with the libexec tools in both cases, so the PPA apptainer needs no `fuse2fs`, `squashfuse_ll` or `fuse-overlayfs` of its own.

## The real binary on the VM

The freeze, validate and unfreeze commands run as one apptainer job. Run with the libexec profile loaded, on the login shell and inside a container started by `condatainer exec`:

- The job froze the fixture, and the archive held every file with its content, including a `0600` file and a mode-000 file.
- `info --validate` passed, and `overlay unfreeze` rebuilt an image with the files.
- `unshare` on the login shell failed in the same session, which is the failure the job route avoids.

What came out of the comparison with the `unshare` route on the development host and the VM:

- **Unreadable files.** The job is a normal user, so it cannot read a file that another uid owns with a mode that excludes the rest.
  - `fuse2fs` refused it, and `mksquashfs` packed it empty with the message `creating empty file` and exit status 0.
  - `fuse2fs -o fakeroot` checks permissions as root and reads it. The pack script fails on `creating empty file`, so a silent loss cannot pass.
- **The host's `/etc`.** Binding a file bound its parent directory. On Ubuntu `/etc/resolv.conf` is a link into `/run`, which the job's root lacks, so apptainer failed. The two system files are bound as files.
- **The exit status of a refusal.** The starter's errors do not exit 255. A failed launch under the restriction is settled by a probe whatever its status.
- **The refusal message.** With the profile unloaded, the first build printed only apptainer's own error. The message now names the apptainer and the cause, and suggests `host_apptainer` when the package apptainer proves it works. Seen on the VM: the suggestion was missing while the toolchain's `bin` was first on `PATH`, because the lookup took that apptainer as the candidate. It skips it now.

## Nested behavior

- An apptainer started inside a container works: plain launch, `--overlay` of a `.sqf` and a `.img`, stacked overlays, and `--fusemount`.
- A sandbox directory that binds the host's `/usr`, `/bin`, `/lib*`, `/etc/passwd` and `/etc/group` works as a root. No image is needed.
- `--overlay` merges the artifact into the container's `/`. A freeze that used it would read the root's own files with the artifact.
- `--fusemount` puts each artifact at a path of its own. The root holds only its own directories.
  - Each fusemount is made before the next, so a later one can use an earlier path as its lower.
  - The spec is split on commas and spaces. Every option is its own `-o`, and lowers are joined with a colon.
  - Apptainer options must come before the image path.
- A `fuse-overlayfs` union of a `.img` over a snapshot works as the third fusemount, with `squash_to_root` and a scratch upper.
- The same `fuse-overlayfs` started from the job's shell fails: no mount capability and no setuid `fusermount3`.
- Without a root map, a squashfs packed `-all-root` shows as the overflow uid 65534.
  - `squashfuse_ll -o uid=<real> -o gid=<real>` shows the real uid. The old clamp came from a namespace that maps only uid 0.
  - `mke2fs -d` records what it sees, so with no option the owner is 65534. With `-o uid -o gid` it records the real owner, and unfreeze needs no `ChownRecursively` pass. Tried on the development host: the owners matched the chown pass.
- Whiteouts survive: a `.img` that deletes a snapshot file with a `0:0` node gives a union that hides it. The packed archive matches the one from the `unshare --map-root-user` route, in listing and contents.
- The reverse direction keeps the node: `squashfuse_ll` of the archive, then `mke2fs -d`, ends with a char node.
- A write-protected `.img` mounted by `fuse2fs` with no `-o ro` is read-only, with `source write-protected, mounted read-only`. The write is refused and the bytes do not change.
- Two jobs at once, each with its own mounts, both finish.
- A bare `mke2fs` image failed as an overlay: apptainer wants `upper/` and `work/` in it. CondaTainer's packed overlays have them.

## Cleanup when the parent dies

- A SIGTERM, a SIGKILL, or a group SIGKILL sent to the apptainer process leaves nothing running. The FUSE servers and the payload all end.
- A SIGKILL to the parent of a plain launch leaves them running, even with `--pdeathsig KILL` on the apptainer.
  - The process the apptainer becomes, `Apptainer runtime parent`, enters its user namespace, and that clears the signal.
  - A control with a plain `sleep` shows the method works.
- A small process that never enters a namespace, starts the apptainer as its child, has `Pdeathsig` and kills the child on it, left nothing behind when its parent was killed. A normal end left nothing behind either.

## A libexec apptainer run directly as the container's command

- The libexec apptainer fails when it is the command of an `apptainer exec`: `failed to resolve session directory /opt/libexec/var/apptainer/mnt/session: lstat /opt: no such file or directory`.
- It works when a shell starts it, in the same container with the same arguments.

| Outer launch | Command inside | Result |
|---|---|---|
| `exec` | the apptainer, directly | fail |
| `exec` | `env <apptainer> ...` (env executes it directly) | fail |
| `exec` | `sh -c "<apptainer> ..."` | pass |
| `exec` | `bash -c "<apptainer> ...; true"` | pass |
| `shell`, command piped in | the apptainer, run from the shell | pass |
| `condatainer exec` | the apptainer, directly | fail |
| `condatainer exec` | `sh -c "<apptainer> ..."` | pass |
| `condatainer exec` | `condatainer exec ...` | pass |

- Interactive `apptainer shell` inside `apptainer shell`, three levels deep, also worked.
- The bundle is built with the prefix `/opt/libexec` and relocates itself from its own path. The built-in prefix is only the fallback.
- Debug output of a failing inner launch finds its config and starter at the real path. Later it reads its own path as `/lib/apptainer/bin/starter`, which does not exist, and falls back to the built-in prefix.
- The mechanism is not found. The trigger is: the apptainer is the process the outer one executes, with no shell between.
  - `bash -c "<apptainer> ..."` with a single command executes it directly, so it fails like the direct case. A second command after it keeps the shell in between.
  - `apptainer buildcfg` run directly prints the relocated paths. Only a launch is affected.
  - A launch by CondaTainer inside a container, with the toolchain mounted at `/.condatainer/libexec`, keeps its session directory under that path and works.
- It does not depend on the roots, the binds, `/tmp`, `APPTAINER_CACHEDIR` or `APPTAINER_TMPDIR`, or on whether the inherited bind variables are set.
- CondaTainer's own nested use is not affected, because the apptainer is started by CondaTainer or by a script.
  - `condatainer exec <path to the libexec apptainer> shell <image>` is affected. It fails with the same session-directory error, because the apptainer is the command.
- With the PPA apptainer as the inner one, a nested launch failed on `/etc/apptainer/apptainer.conf` and then on its session directory `/var/lib/apptainer/mnt/session`, because the test root did not bind them. CondaTainer binds the libexec apptainer into containers, not the host's, so the row is skipped.
- On the VM, under AppArmor with the profile loaded, the libexec apptainer started by a shell inside a container launched another container.

## Other observations

- **The kernel log line `overlayfs: upper fs missing required features`.** It appears when a `.img` overlay is mounted. The mount still works, through `fuse-overlayfs`.
- **The `fuse2fs` journal warning.** The `.img` is ext3 and `fuse2fs` does not use the journal. It is printed on every mount.

## Reproduction

1. On an Ubuntu host with the restriction on, run `unshare -r true`. It fails with the `uid_map` message.
2. Run the libexec apptainer on any image. It fails with the `setgroups` message.
3. Install a profile for the libexec paths with `userns,` and `flags=(unconfined)`, and load it with `apparmor_parser -r`. The same launch works.
4. Remove `flags=(unconfined)` and load it again. The launch fails at the shared libraries.
