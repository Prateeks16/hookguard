# Build a static console binary (pure-Go SQLite, no CGo needed), then ship it
# on a minimal distroless base — mirrors the gateway's Dockerfile.
#
# Build context must be the repo root, not web/, because web/go.mod uses
# `replace hookguard => ../` to reuse gatewaysig (DESIGN.md §7.2): e.g.
#   docker build -f web/Dockerfile -t hookguard-console .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY web/go.mod web/go.sum ./web/
RUN go mod download && (cd web && go mod download)
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/console ./web/cmd/console

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/console /console
EXPOSE 7000
USER nonroot:nonroot
ENTRYPOINT ["/console"]
