---
title: Running Issues
description: Fix a container, freeze or build that fails because the system blocks user namespaces, or because a build's fakeroot needs a newer glibc.
---

## Cannot create a user namespace

A user namespace is a kernel feature that lets a normal user run a container, or mount an image, without root. A command that starts a container, such as `condatainer exec`, or one that mounts an image, such as `overlay freeze`, fails when the system refuses it.

CondaTainer names the Apptainer that was refused. Its last line says what an administrator changes, and that line tells you which case you are in:

| The message ends with | Case |
|---|---|
| `add it to the AppArmor allowlist, or disable the restriction with kernel.apparmor_restrict_unprivileged_userns=0` | [Ubuntu 24.04 and later](#ubuntu-2404-and-later) |
| `raise user.max_user_namespaces above 0` | [The limit is 0](#the-limit-is-0) |
| `set kernel.unprivileged_userns_clone=1` | [Normal users are not allowed](#normal-users-are-not-allowed) |
| `allow unprivileged user namespaces` | [Another cause](#another-cause) |

- When another Apptainer on your `PATH` can start containers here, the message adds a line with the command that selects it. The last line then starts with `Or an administrator can`.
- Apptainer's own message for the same refusal is `FATAL:   Could not write info to setgroups: Permission denied`. You see it when you run Apptainer yourself.

### Ubuntu 24.04 and later

Ubuntu blocks user namespaces by default. Its security layer, AppArmor, refuses them to programs that have no AppArmor profile, and the Apptainer in the CondaTainer toolchain has none.

```text
[CNT✗] the system blocks user namespaces for /opt/condatainer/libexec/bin/apptainer.
  An administrator can add it to the AppArmor allowlist, or disable the restriction with kernel.apparmor_restrict_unprivileged_userns=0
```

Check the setting:

```bash
sysctl kernel.apparmor_restrict_unprivileged_userns
```

- `= 1` means the system blocks user namespaces.
- `= 0` means it does not, and the message has another cause.
- `No such file or directory` means the system has no such restriction.

Use the first way that works:

1. **Use the Apptainer package.** The `apptainer` package from the Apptainer PPA comes with an AppArmor profile, so it runs with no change. Point CondaTainer at it:

   ```bash
   condatainer config set host_apptainer /usr/bin/apptainer
   ```

   - CondaTainer then runs containers and builds `.def` files with that Apptainer, outside a container.
   - Unset, no host Apptainer is used.
   - If one is on your `PATH` and works, the message prints this command for you:

   ```text
   [CNT✗] the system blocks user namespaces for /opt/condatainer/libexec/bin/apptainer.
     Use the host apptainer instead: condatainer config set host_apptainer /usr/bin/apptainer
     Or an administrator can add it to the AppArmor allowlist, or disable the restriction with kernel.apparmor_restrict_unprivileged_userns=0
   ```

2. **Ask an administrator for a profile.** It covers the one Apptainer named in the message, and other programs stay restricted. See [Site Install](../deployment/site_install.md#ubuntu-2404-and-later) for the steps. Run the command that failed again once the profile is loaded.

3. **Turn the restriction off.** An administrator can turn it off for the whole machine:

   ```bash
   sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
   ```

   To keep the change after a reboot:

   ```bash
   echo 'kernel.apparmor_restrict_unprivileged_userns=0' | sudo tee /etc/sysctl.d/60-userns.conf
   ```

   - The setting applies to every program on the machine, and only to that machine. Set it on each node that runs containers.
   - On a shared machine you cannot change it. Ask the administrators.

### The limit is 0

The kernel allows no user namespaces at all.

```text
[CNT✗] the system blocks user namespaces for /opt/condatainer/libexec/bin/apptainer.
  An administrator can raise user.max_user_namespaces above 0
```

Check the setting:

```bash
cat /proc/sys/user/max_user_namespaces
```

- `0` is the cause.
- Ask an administrator to raise it, for example with `sysctl -w user.max_user_namespaces=15000`.
- An Apptainer installed setuid does not need user namespaces. If the host has one, select it with `condatainer config set host_apptainer <path>`.

### Normal users are not allowed

Some kernels have a switch that lets only root create user namespaces.

```text
[CNT✗] the system blocks user namespaces for /opt/condatainer/libexec/bin/apptainer.
  An administrator can set kernel.unprivileged_userns_clone=1
```

Check the setting:

```bash
cat /proc/sys/kernel/unprivileged_userns_clone
```

- `0` is the cause. The file does not exist on most systems.
- Ask an administrator to set it to `1`, for example with `sysctl -w kernel.unprivileged_userns_clone=1`.

### Another cause

None of the three settings above is on, so the cause is another limit, such as SELinux or a filter in the container runtime that starts your session.

```text
[CNT✗] the system blocks user namespaces for /opt/condatainer/libexec/bin/apptainer.
  An administrator can allow unprivileged user namespaces
```

Ask the administrators of the machine. They know which limit is in use.

## A build fails with a GLIBC error from faked

```text
/.singularity.d/libs/faked: /lib/x86_64-linux-gnu/libc.so.6: version `GLIBC_2.38' not found
fakeroot: error while starting the `faked' daemon.
```

Building an operating-system overlay from a `.def` file runs `fakeroot`, a tool that lets the build act as root without being root. Apptainer copies it into the build. If the copy comes from a system newer than the image being built, the image's C library cannot run it.

- Run `condatainer update --libexec`. The toolchain carries a fakeroot that runs in any image, and builds use it.
- If the toolchain is installed, unset `host_apptainer` with `condatainer config remove host_apptainer`, so builds use the toolchain's Apptainer.
- CondaTainer then ends with this message, which gives the same advice:

```text
[CNT✗] the fakeroot bound into the build needs a newer glibc than the image has: run `condatainer update --libexec`, which installs a fakeroot that runs in any image
```
