---
title: "0019. Web source under web/ and Cloudflare project under cloudflare_site/"
status: Accepted
relates_to:
  - contributor-guidance
topics:
  - repository-layout
  - ci
  - cloudflare
---

# 0019. Web source under `web/` and Cloudflare project under `cloudflare_site/`

Date: 2026-04-15

## Status

Accepted

## Context

This repository mixes design documents, Go CLI code (`cmd/`), and a small **browser-delivered** surface (today the interactive document graph). That surface was previously rooted at `docs/mindmap.html` while Cloudflare Wrangler lived under `site/`, which read as a generic “website” folder rather than “Cloudflare deploy boundary,” and blurred documentation versus deployable HTML. Separating **`web/`** (browser source) from **`cloudflare_site/`** (Wrangler + deploy-time static) makes layout and CI obvious for contributors.

## Decision

1. **Browser-oriented source** (static HTML today; future Vite entrypoints and assets) lives under **`web/`**, starting with the document graph as **`web/public/index.html`** (served at `/` in production). **Node tooling** (`package.json`, lockfile, and scripts such as `npm run dev` / `npm run build`) stays at the **repository root** so day-to-day work does not require `cd web`; Vite config may still point `root` at `web/` (or a subdirectory) for resolution.
2. The **sole Wrangler project** in this repository lives under **`cloudflare_site/`** (`wrangler.toml`, `public/` populated by CI from the Build Site artifact, plus any future Worker source in that tree). The GitHub Actions **Deploy Site** workflow continues to use **`workingDirectory: cloudflare_site`** and unpacks the artifact into **`cloudflare_site/public/`**; workflow **names** (`Build Site`, artifact `site`, environments `site-preview` / `site-production`) stay stable for existing automation and operators.
3. **Build Site** assembles **`_site/`** from `web/` (and later from JS build outputs) without changing that deploy contract.

## Consequences

- Contributors edit the live mindmap at **`web/public/index.html`** instead of **`docs/mindmap.html`**.
- Paths in runbooks and local Wrangler preview use **`cloudflare_site/`** instead of **`site/`**.
- Future SPA work adds **Vite** (and related devDependencies) using the **root** `package.json`, with source and config under **`web/`**, and extends **`site-build.yml`** to merge build output into **`_site/`** without renaming **`cloudflare_site/`** again.
- Links and docs that referred to `site/wrangler.toml` or `docs/mindmap.html` must be updated when backporting older instructions.
