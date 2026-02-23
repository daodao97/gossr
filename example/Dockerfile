# syntax=docker/dockerfile:1.7

FROM node:22-alpine AS frontend
WORKDIR /app/example/web

COPY example/web/package.json example/web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

COPY example/web ./
RUN npm run build

FROM golang:1.25-alpine AS backend
WORKDIR /app

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
COPY --from=frontend /app/example/web/dist ./example/web/dist

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -tags nov8 -o /out/server ./example

FROM alpine:3.21 AS final
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

ENV GIN_MODE=release

COPY --from=backend /out/server /app/server

EXPOSE 8080

ENTRYPOINT ["/app/server"]
