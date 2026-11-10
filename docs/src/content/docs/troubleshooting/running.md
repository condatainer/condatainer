---
title: Running Issues
description: Fix a container, freeze or pack that fails because the system blocks user namespaces.
---

## Cannot create a user namespace

```text
could not write info to setgroups: Permission denied
```

```text
unshare: write failed /proc/self/uid_map: Operation not permitted
```

The first message comes from a command that starts a container, such as `condatainer exec`. The second comes from `overlay freeze`, which runs `unshare`. Both happen on Ubuntu 24.04 and later, which blocks user namespaces by default.

A user namespace is a kernel feature that lets a normal user run a container, or mount an image, without root. Ubuntu's security layer, AppArmor, refuses it to programs that have no AppArmor profile, and CondaTainer's tools have none.

Check the setting:

```bash
sysctl kernel.apparmor_restrict_unprivileged_userns
```

- `= 1` means the system blocks user namespaces.
- `= 0` means it does not, and the message has another cause.
- `No such file or directory` means the system has no such restriction.

Turn the restriction off:

```bash
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
```

To keep the change after a reboot:

```bash
echo 'kernel.apparmor_restrict_unprivileged_userns=0' | sudo tee /etc/sysctl.d/60-userns.conf
```

- Run the command that failed again.
- The setting applies to every program on the machine, and only to that machine. Set it on each node that runs containers.
- On a shared machine you cannot change it. Ask the administrators to set it to `0`.
