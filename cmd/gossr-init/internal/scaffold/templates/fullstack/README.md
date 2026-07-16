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

Open <http://127.0.0.1:8080>. Add page payload handlers to the isolated `DataEngine` in `main.go`. Authentication belongs to the host application and can be injected with `gossr.Options.SessionResolver`; the generated project does not provide a default identity policy.
