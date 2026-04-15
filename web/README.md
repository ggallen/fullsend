# Web

Browser-delivered assets for the public site: static files under `public/` today (document graph as `index.html`), with a future single Vite app expected to build into deployable output that CI merges into `_site/` for Cloudflare. **`package.json` / `npm run dev` stay at the repository root** (Vite can still use this tree as its source root). Wrangler configuration and the Worker live only under [`../cloudflare_site/`](../cloudflare_site/).
