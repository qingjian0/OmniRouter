# OmniRouter Admin UI

The admin console is a Vue 3 + TypeScript single-page application. Vite writes
production assets to `internal/admin/static/`, where the Go server embeds them
into the executable.

## Development

```sh
npm install
npm run dev
```

The dev server is for UI development only. Vite proxies `/healthz`, `/readyz`,
`/admin`, `/v1`, and `/v1beta` to `http://127.0.0.1:8080`. Start the Go gateway
on that address, then open the Vite URL (normally `http://127.0.0.1:5173`).

## Production build

```sh
npm run typecheck
npm run build
```

Node.js is a build-time requirement only. The release binary serves the
embedded assets and does not spawn Node.js.

The repository keeps a minimal source-only fallback page in
`internal/admin/fallback/`. CI and release workflows always build the Vue
application and replace the embedded asset directory before compiling the
production binary.
