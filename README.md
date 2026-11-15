<h1 align="center">Web Search</h1>

<p align="center">
  <strong>Search torrent sites with packaged or custom templates and enqueue results.</strong>
</p>

<p align="center">
  <a href="https://github.com/krate-apps/fleet"><img src="https://img.shields.io/badge/Fleet-official%20extension-0B1220?logo=go&logoColor=white" alt="Fleet official extension" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT" /></a>
  <img src="https://img.shields.io/badge/version-1.0.0-informational" alt="Version 1.0.0" />
  <img src="https://img.shields.io/badge/engine-rTorrent-brightgreen" alt="rTorrent" />
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="https://github.com/krate-apps/fleet">Fleet</a> ·
  <a href="https://github.com/krate-apps/fleet-official-bucket">Official bucket</a> ·
  <a href="https://github.com/krate-apps/fleet-official-bucket/blob/main/extensions.md">All extensions</a> ·
  <a href="https://github.com/krate-apps/fleet/blob/main/docs/developer/create-extension.md">Create an extension</a>
</p>

---

<p align="center"><code>org.fleet.websearch</code> · official Fleet extension · SDK worker package</p>

## Install

Requires a running [Fleet](https://github.com/krate-apps/fleet) host (rTorrent via SCGI).

### From the Official bucket

1. Enable the **Fleet Official** bucket (`org.fleet.official`) if it is not already on.
2. Open **Extensions** → find **`org.fleet.websearch`** → Install.
3. Configure from the extension’s page / Configure UI.

Listings live in [`fleet-official-bucket`](https://github.com/krate-apps/fleet-official-bucket) (`bucket/org.fleet.websearch.json`).  
`package.url` / `sha256` are filled when a GitHub Release asset is published.

### Local development

```bash
make build
```

Place the built worker under Fleet’s `extensions-src/` (or install from the Official bucket).

## Package layout

```text
fleet-ext-websearch/
  fleet.ext.yaml          # manifest (id, capabilities, frontend/backend)
  ui/extension.json       # declarative UI
  bin/*-worker            # JSON-RPC worker (stdin/stdout), when present
  cmd/                    # Go worker sources, when present
  LICENSE
```

## Related

| | |
| --- | --- |
| Host | [krate-apps/fleet](https://github.com/krate-apps/fleet) |
| Docs | [Create an extension](https://github.com/krate-apps/fleet/blob/main/docs/developer/create-extension.md) |
| All extensions | [Catalogue](https://github.com/krate-apps/fleet-official-bucket/blob/main/extensions.md) |
| Bucket listing | [fleet-official-bucket/bucket/org.fleet.websearch.json](https://github.com/krate-apps/fleet-official-bucket/blob/main/bucket/org.fleet.websearch.json) |
| Manifest API | `>=1.0.0 <2.0.0` |

---

<p align="center">
  Developed by the <a href="https://runkrate.com">Krate</a> team.<br />
  Released under the MIT License.
</p>

## Pack / release

```bash
make pack   # dist/<id>-linux-amd64.tar.gz + dist/SHA256SUMS
```

Tag `v*` to run `.github/workflows/release.yml` (uploads archive + `SHA256SUMS`).
