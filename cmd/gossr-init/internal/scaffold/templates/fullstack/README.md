# __GOSSR_PROJECT_NAME__

A complete Gin + gossr + Vue SSR application.

## Production build

```bash
cd web
npm install
npm run build
cd ..
go mod tidy
go run .
```

Open <http://127.0.0.1:8080>.

## Development

Run the frontend:

```bash
cd web
npm run dev
```

In another terminal, run the Go host:

```bash
DEV_MODE=1 DEV_SERVER_URL=http://127.0.0.1:3333 go run .
```

Open <http://127.0.0.1:8080>. `main.go` implements one `resolvePage` function —
the single boundary that produces the page document for both the initial HTML
document and in-app navigation. Grow it per route (auth redirects, data
loading, not-found) and mirror the document shape in `web/src/page-document.ts`.
Authentication belongs to the host application: resolve the session in
middleware or inside the resolver; the generated project does not provide a
default identity policy.
