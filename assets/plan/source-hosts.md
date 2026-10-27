# Plan: recipe sources on private git hosts, and ordering a lower layer's sources

Status: deferred, not urgent. Nothing here is implemented. Only GitHub's raw host is tested. Every other host is
read as `base/path` with `Authorization: Bearer`. `config source move` acts on one layer's own list.

## Goal

A private recipe source on a git host other than GitHub opens with one `config source add`, without a
change to the stored entry. The entry stays `name: link`, and the token stays in `credentials.json`.

A person can also put a source that a lower layer lists first in their own search order, by its name.

## What each host documents

Only the pages named here were read. Nothing was run against a live instance.

| Host | Token header | File route |
|---|---|---|
| GitHub (`raw.githubusercontent.com`) | `Authorization: Bearer` | `<owner>/<repo>/<ref>/<path>`, flat. Works today. |
| GitLab | `PRIVATE-TOKEN: <token>` is recommended. `Authorization: Bearer <token>` is documented for personal, project and group access tokens. | `GET /projects/:id/repository/files/:file_path/raw?ref=<ref>`, with the file path URL-encoded as one segment. |
| Gitea | `Authorization: token <key>`. The word `token` is required "for historical reasons". `Bearer` is documented for OAuth2 tokens only. | `GET /repos/{owner}/{repo}/raw/{filepath}`, with `ref` as a query parameter. The page says `filepath` takes the form `{ref}/{filepath}`. `/media/{filepath}` returns the file or its LFS object. |
| Forgejo, Codeberg | The same as Gitea. Codeberg's token page defers to Forgejo's. | The same as Gitea, not read separately. |
| Bitbucket Cloud | `Authorization: Bearer` for repository access tokens. | Not read. Not planned. |

The query-string `token=` and `access_token=` forms are documented for OAuth2 tokens only, and are not used.

## Design

### Token scheme

- `config source add <name> <link> --token-scheme token|bearer`. The default is `bearer`, so GitHub, GitLab and every
  existing source behave as they do now.
- The scheme is stored beside the token, as an optional `scheme` field of the source's entry in `credentials.json`.
  An older build ignores it, because the file keeps fields it does not know.
- `catalog.Token` carries the scheme. `authorize` sends `<scheme> <secret>`.
- Only Gitea, Forgejo and Codeberg need `token`.

### Link template

- A link that contains `{path}` has it replaced by the file's path, and text after it stays.
  `{path}` keeps the slashes. `{path_encoded}` writes them as `%2F`.
- A link with neither is read as today: `base`, a `/`, then the path.
- The template is needed where the route puts the path in the middle or ends in a query. GitLab is one. Gitea is
  another when a `ref` query is wanted.

Links:

| Host | Link |
|---|---|
| GitHub | `https://raw.githubusercontent.com/<owner>/<repo>/<ref>` |
| GitLab | `https://<host>/api/v4/projects/<id>/repository/files/{path_encoded}/raw?ref=<ref>` |
| Gitea, Forgejo, Codeberg | `https://<host>/api/v1/repos/<owner>/<repo>/raw/{path}?ref=<ref>` |

Gitea's page documents the path as `{ref}/{filepath}`. `https://<host>/api/v1/repos/<owner>/<repo>/raw/<ref>` with
no template follows that form. Which of the two a live instance serves is open.

### What stays out of the entry

- The scheme and the token are looked up by the link, nearest layer first. A copy of a source in another layer has
  the same link and finds both.
- Nothing about the host is written into `config.yaml`, which every user of a shared layer reads.
- No built-in presets such as `--type gitea`. Each would fix a route that has to be checked per host first. A preset
  can be added later over the scheme and the template without changing stored data.

### Ordering a lower layer's source

- The search order is by layer first (`user`, `extra-root`, `cnt-root`), then by the order of each layer's list.
  Anything in a person's layer is searched before a group's or the site's.
- `config source move <name> --first` in a layer that does not list `<name>` copies the `name: link` entry from the
  nearest lower layer that does, then places it. This is already how the default `cnt` is moved.
- The lower layer's list is not touched, so the precedence a group shares does not change for anyone else.
- The copy carries only the name and the link. The token and the scheme are looked up by the link, so the copy finds
  them in the lower layer's `credentials.json` when the person can read it. A site file that is not group-readable
  holds nothing for them.
- Without `-l`, `move` writes where `config set` writes, not into the layer that lists the name.
- A copy keeps the link it had. If the lower layer later changes the link, the copy shadows the change until it is
  removed with `config source remove`.
- An alternative is an entry that is only a name, resolved from the lower layer when read. It never goes stale, but it
  changes the entry shape, which is a schema change.
- A header or scheme kept inline in the entry would need the copy to carry more than name and link.

## Decisions to check before the first stable release

- `scheme` is the only stored field the host work adds. It is optional.
- `{path}` and `{path_encoded}` are reserved in a source link.
- Ordering by name adds no stored field. Its one choice is a copy of the entry against a name-only reference.

## Not verified

- Gitea's and GitLab's file routes on a live instance, and Bearer with a Gitea personal token.
- Whether GitLab's web `-/raw/` route takes a token.
- The `/api/v1` prefix of Gitea. The page read names routes without it.
- Bitbucket's file route.
