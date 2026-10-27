# Writing the docs

## Audience

- Readers know nothing about CondaTainer, and very little about containers. They know the basics of Conda, and of HPC: a scheduler, jobs, login and compute nodes.
- Explain every term at its first use, in one plain clause: container, overlay, recipe, recipe source, file quota, `PATH`, environment variable.
- Readers go through the pages in order. Explain a term once, on the earliest page that uses it.
- Explain on a page every term it relies on. A link to another page adds depth on top.

## Style

- What the user does and sees: commands, their effect, what is refused and how to proceed.
- For each option, state what it does, and let the reader choose.
- Write for any cluster: state what CondaTainer does, and leave the site's own setup to the reader.
- Use `create`, the command's own name. Use "installed" for what `list` shows.
- Reach data through the environment variables an overlay sets.
- Show an overlay's variables inside the container: in its shell, in a script, or in `bash -c '...'`, where the container's shell expands them.
- Show a command that serves something picking a free port, or taking one with `--port`.
- Use real tools and data sets as examples, with placeholders for hosts, users and numbers.
- Keep Get started pages and guides simple: what to run and what happens. Implementation detail, such as file layouts and the paths overlays are mounted at, goes on an advanced page.
- Short sections, commands first, then one bullet per fact.
- Wrap a long command over several lines with `\`.
- Describe the present. When behavior changes, edit the part that is now wrong.
- Start every section with its content.
- State what happens. Leave out what does not happen.

## Accuracy

- Check every claim against the current code or the CLI's `--help`, and write only what is checked.
- Change only what was asked.
