# Docs

The site is built with [Starlight](https://starlight.astro.build). Pages are Markdown files in `src/content/docs/`. Add a new page to `sidebar` in `astro.config.mjs`. A page with tabs or other components is an `.mdx` file, and links to it end in `.mdx`.

## Headings in the sidebar

- The page's headings are listed in the left sidebar, under the page's entry. There is no right-hand column.
- `plugins/sidebar-toc/` does this. Its `minHeadingLevel` and `maxHeadingLevel` options in `astro.config.mjs` set which headings are listed.
- A page overrides them with `tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 4 }` in its frontmatter, or turns the list off with `tableOfContents: false`.

## Code tabs

- Code blocks that carry the same `tabs="<group>"` attribute, one after another, become one group. The file names of the blocks are tabs in the code frame's header, where a single block shows its file name.
- `plugins/code-tabs/` does this: `index.mjs` groups the blocks, and `client.js` builds the tabs in the browser. It works in `.mdx` pages only.

````md
```bash title="src/00_download.sh" tabs="scripts"
...
```

```bash title="src/01_trim.sh" tabs="scripts"
...
```
````

## Build

Requires Node.js 22.12 or later.

```bash
cd docs
npm ci
npm run build       # static site in dist/
```

## Preview

```bash
npm run serve               # build, then serve dist/ on port 4321
npm run serve -- --port <n> # use another port
npm run serve:dev           # live reload
```

- Both serve on port 4321 and print the address to open. Stop with Ctrl+C.
- If the port is in use, they stop and say so. Pick a free one with `--port <n>`.
- Under code-server, the address is its `/absproxy/<port>/` path. `serve:dev` does not work there because of Vite dev loads files from `/`.
- On a remote host without code-server, forward the port first: `ssh -L <port>:127.0.0.1:<port> <host>`.

## Publish

- A push to `main` that changes `docs/` deploys the latest docs to GitHub Pages.
- Read the Docs builds each release tag.
