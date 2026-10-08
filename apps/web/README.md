# apps/web — FundZim web frontend

Next.js 16 (App Router) · React 19 · TypeScript · Tailwind CSS v4. Scaffolded with `create-next-app` in
Stage 0; no FundZim UI exists yet.

This app is **presentation only**: it renders pages and calls the Go API at `/api/v1/`. It never talks to the
database, never contains business or financial logic, and never holds secrets. Standards:
[docs/FRONTEND.md](../../docs/FRONTEND.md). Read [AGENTS.md](AGENTS.md) before writing Next.js code — this
Next.js version has breaking changes relative to older documentation.

```bash
npm ci          # install from lockfile
npm run dev     # http://localhost:3000
npm run build
npm run lint
```
