# __GOSSR_PROJECT_NAME__

Generated from the gossr `__GOSSR_TEMPLATE__` Vue template.

```bash
npm install
npm run dev       # Vite on 127.0.0.1:3333
npm run typecheck
npm run build     # dist/client + dist/server/server.js
```

The generated `embed.go` exposes `Dist`. Wire it into the host Go application:

```go
type homePayload map[string]any

func (payload homePayload) AsMap() map[string]any { return payload }

data := gossr.NewDataEngine()
data.GET("/", gossr.WrapSSR(func(c *gin.Context) (gossr.SSRPayload, error) {
    return homePayload{
        "message": "Hello from Go",
        "path": c.Request.URL.Path,
    }, nil
}))

if err := gossr.SsrWithOptions(router, web.Dist, gossr.Options{
    DataEngine: data,
}); err != nil {
    log.Fatal(err)
}
```

Replace `web` with the import name of this directory. In development, run the Go server with `DEV_MODE=1 DEV_SERVER_URL=http://127.0.0.1:3333`, then run `npm run dev` here. The Vite proxy intentionally keeps `changeOrigin: false` so gossr's same-origin authorization accepts `/_ssr/data` requests.

Project policy belongs in the host application: authentication through `SessionResolver`/middleware, data permissions in Go handlers, and application-specific locale rules. The generated files only implement the rendering protocol.
