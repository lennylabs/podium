# Multi-stage build: a fat Go builder, a minimal distroless runtime.
#
# Build args wire through to internal/buildinfo so `podium-server
# version` inside the container reports a real version. The release
# workflow sets these from the git tag; a local `docker build` without
# overrides ends up with the package defaults.
#
#   docker build -t podium-server:dev .
#   docker build --build-arg VERSION=v0.1.0 \
#                --build-arg COMMIT=$(git rev-parse --short HEAD) \
#                --build-arg DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
#                -t podium-server:0.1.0 .

ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS build

# pkg/vector and the standalone-server bootstrap depend on
# sqlite-vec-go-bindings/cgo and mattn/go-sqlite3, both CGO-only.
# Install gcc + musl-dev so CGO can compile, then static-link with
# musl so the resulting binary runs on distroless-static (no libc).
RUN apk add --no-cache gcc musl-dev

WORKDIR /src

# Cache the module download layer separately from the source layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags "-s -w -linkmode external -extldflags '-static' \
        -X github.com/lennylabs/podium/internal/buildinfo.Version=${VERSION} \
        -X github.com/lennylabs/podium/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/lennylabs/podium/internal/buildinfo.Date=${DATE}" \
      -o /out/podium-server \
      ./cmd/podium-server

# Runtime: distroless static. No shell, no package manager, no setuid
# binaries. Runs as a non-root user (uid 65532) baked into the image.
# The static-linked musl binary above runs here without a libc.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/podium-server /usr/local/bin/podium-server

# §13.10 standalone default. Override via PODIUM_BIND.
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/podium-server"]
