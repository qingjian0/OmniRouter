# OmniRouter Admin UI

The admin console is a Vue 3 + TypeScript single-page application. Vite writes
production assets to `internal/admin/static/`, where the Go server embeds them
into the executable.

## Development

```sh
npm install
npm run dev
```

The dev server is for UI development only. API calls use same-origin paths and
expect the Go gateway to be available at the same origin or a configured Vite
proxy (see below).

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
