# Docs content layout

This directory is not part of the docs site. Fumadocs ingests everything under
`docs/` only, so keep files that must not become pages out of that directory.
`docs/content/docs/` holds the pages; this directory holds the records about them.

## Versions

```text
docs/content/docs/
  meta.json            lists the version roots: current first, then archives newest first
  current/             the live docs, served at /docs/<page>
  vX.Y/                one frozen archive per retired release line, served at /docs/vX.Y/<page>
docs/content/archive-provenance.json   where each archive came from
```

`current/meta.json` carries the line it documents as its title. That line has
no archive until `current/` moves on to the next one.

## Archives are immutable

An archive is a byte-for-byte copy of `current/` as it was at the last release
tag of its line. Nothing edits it afterwards, because the point of an archive
is to show what that release shipped with. A mistake found later is fixed in
`current/`.

Each archive has an entry in `archive-provenance.json` with `sourceTag`,
`sourceCommit`, `sourcePath` and `sourceTree`. `scripts/docs-archive-config-test.sh`
checks that the committed archive is exactly that tree, that the tag resolves
to that commit, and that the tag ends its line. Cut one with
`npm run docs:archive -- <tag>`; the steps are in `RELEASING.md`.

## How archives are published

- Links inside an archive keep the form they had at release (`/authentication`).
  The `remarkVersionedLinks` plugin in `docs/source.config.ts` scopes them to
  `/vX.Y/...` at build time, so the copy stays identical to the tag.
- Every archived page is `noindex`, shows an "archived, unsupported" notice
  linking to the same page in the current docs, and is left out of the sitemap.
- The static export puts an archive's pages at `docs/out/vX.Y/<page>.html` and
  its index at `docs/out/vX.Y.html`; `npm run docs:export-contract` checks both.
- Analytics counts archived paths under `/_other`; the allowlist in
  `analytics/src/contract.ts` covers current pages only.
