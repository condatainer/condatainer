---
title: Helper Issues
description: Fix a helper that cannot be reached, cannot install packages, fails on one node, or cannot start.
---

## The dashboard cannot reach a helper

```text
cannot reach <node>: ...
```

The helper job is running, but its page does not open, and the [dashboard](../guides/helper.mdx#web-dashboard) shows the message above. `<node>` is the compute node the helper runs on.

Choose how the dashboard reaches the compute node with `helper.connect`:

```bash
condatainer config set helper.connect scheduler
```

| Value | What the dashboard does |
|---|---|
| `auto` (default) | Tries SSH, then the scheduler. |
| `ssh` | Uses SSH only. The login node must be able to log in to the compute node without a password prompt. |
| `scheduler` | Goes in through the scheduler's own command, with no SSH and no open port. Slurm only. The compute node must read the same `condatainer` binary at the same path. |
| `direct` | Connects to `<node>:<port>`. The service listens on every network interface of the compute node, and the login node must be able to reach its ports. |

- A helper reads the value when it starts. Start a new helper to apply a change.

## A helper cannot install extensions or packages

On some clusters the compute nodes have no internet access, so installing an extension in code-server or a package in RStudio fails inside the helper.

Give helpers a proxy through the login node:

```bash
condatainer config append scheduler.proxy_per_job helper
```

- Start a new helper to apply the change.
- The compute nodes need passwordless SSH back to the login node.

See [Proxy](../guides/proxy.mdx) for a shared proxy and for the other job kinds.

## A helper fails on one node

A misconfigured node, or one with a very slow connection to storage, can make a helper unusable. code-server allows itself 10 seconds to start, and on a slow file system it exits with `timed out`.

The **Jobs** and **History** tabs of the dashboard show the node a helper ran on. Keep jobs off it:

```bash
condatainer config append scheduler.exclude_nodes <node>
```

- Use the **Node** value on the helper's detail page, as your scheduler spells it.
- Start a new helper to apply the change.
- The setting applies to every job CondaTainer submits.
- To allow the node again: `condatainer config remove scheduler.exclude_nodes <node>`.
- PBS does not support excluding nodes.

## The scheduler does not work yet

A site moving from a single powerful server to a cluster may install the scheduler on the server before it runs jobs. Helpers then fail to start. Turn off job submission to run them on the server itself:

```bash
condatainer config set scheduler.submit_job false
```

- Builds and `condatainer run` also run on this machine instead of submitting jobs.
- When the scheduler works, turn it back on: `condatainer config set scheduler.submit_job true`.
