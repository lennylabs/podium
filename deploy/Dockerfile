# Multi-stage Dockerfile for the podium-server binary.
# Standard topology per spec §13.1.
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git build-base
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG GOOS=linux
ARG GOARCH=amd64
ENV CGO_ENABLED=1
RUN go build -trimpath -ldflags='-s -w' -o /out/podium ./cmd/podium && \
    go build -trimpath -ldflags='-s -w' -o /out/podium-mcp ./cmd/podium-mcp && \
    go build -trimpath -ldflags='-s -w' -o /out/podium-server ./cmd/podium-server

FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs && \
    addgroup -S podium && adduser -S podium -G podium

COPY --from=builder /out/podium        /usr/local/bin/podium
COPY --from=builder /out/podium-mcp    /usr/local/bin/podium-mcp
COPY --from=builder /out/podium-server /usr/local/bin/podium-server

USER podium
WORKDIR /home/podium

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/podium-server"]
