# __GOSSR_PROJECT_NAME__

Generated from the gossr `__GOSSR_TEMPLATE__` Vue template.

```bash
npm install
npm run dev       # Vite on 127.0.0.1:3333
npm run typecheck
npm run build     # gossr-build: staged dual build + verify + goja smoke + atomic publish
```

`npm run build` runs the gossr build pipeline (`gossr-build`): staged Vite
builds, artifact verification, a real goja render smoke against
`testdata/smoke-snapshot.json`, and an atomic publish into `dist/`. The
smoke invokes `go run github.com/daodao97/gossr/cmd/gossr-smoke`, so run
`go mod tidy` at the host Go module root once before the first build.

The generated `embed.go` exposes `Dist`. Wire it into the host Go application
with one `PageResolver` — the same resolver serves the initial document and
in-app navigation:

```go
func resolvePage(_ context.Context, request gossr.PageRequest) (gossr.PageResult, error) {
    payload, err := gossr.ObjectPayload(map[string]any{
        "schema_version": 1,
        "url":            request.URL.RequestURI(),
        "page": map[string]any{
            "kind":         "home",
            "message":      "Hello from Go",
            "generated_at": time.Now().Format(time.RFC3339),
        },
    })
    if err != nil {
        return gossr.PageResult{}, err
    }
    return gossr.PageResult{Payload: payload}, nil
}

runtime, err := gossr.New(gossr.Config{Bundle: web.Dist})
if err != nil {
    log.Fatal(err)
}
if err := runtime.MountGin(router, gossr.GinOptions{Resolver: resolvePage}); err != nil {
    log.Fatal(err)
}
```

Replace `web` with the import name of this directory. In development, run the Go server with `DEV_MODE=1 DEV_SERVER_URL=http://127.0.0.1:3333`, then run `npm run dev` here. The Vite proxy intentionally keeps `changeOrigin: false` so gossr's same-origin authorization accepts `/_ssr/data` requests.

Project policy belongs in the host application: authentication in middleware or the resolver, data permissions in Go, and application-specific locale rules. The generated files only implement the rendering protocol — pages read the current document with `usePage()` from `src/composables.ts` and never touch SSR, hydration, or navigation plumbing.
