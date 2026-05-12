# Multi-stage build: a Go builder on debian (glibc) + a distroless
# runtime that ships the same glibc.
#
# Why debian, not alpine: sqlite-vec.c uses BSD type names like
# u_int8_t that glibc provides but musl (alpine's libc) does not.
# A musl builder fails to compile sqlite-vec; a glibc builder
# handles it cleanly. The runtime image picks distroless/base
# (glibc) instead of distroless/static (no libc) to match.
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

FROM golang:${GO_VERSION}-bookworm AS build

# build-essential gives us gcc + the cgo toolchain;
# libsqlite3-dev provides sqlite3.h for sqlite-vec.c.
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*

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

# CGO_ENABLED=1 for sqlite-vec + mattn/go-sqlite3. Binary dynamic-
# links against glibc; distroless/base in the runtime stage carries
# the same glibc.
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags "-s -w \
        -X github.com/lennylabs/podium/internal/buildinfo.Version=${VERSION} \
        -X github.com/lennylabs/podium/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/lennylabs/podium/internal/buildinfo.Date=${DATE}" \
      -o /out/podium-server \
      ./cmd/podium-server

# Runtime: distroless base (glibc, no shell, no package manager, no
# setuid binaries). Runs as a non-root user (uid 65532) baked into
# the image.
FROM gcr.io/distroless/base-debian12:nonroot

COPY --from=build /out/podium-server /usr/local/bin/podium-server

# §13.10 standalone default. Override via PODIUM_BIND.
EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/podium-server"]
