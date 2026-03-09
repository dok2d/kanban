# ---- Build stage ----
FROM docker.io/library/golang:1.22-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags='-s -w -extldflags "-static"' -tags 'sqlite_omit_load_extension' -o /kanban ./cmd/server

# ---- Runtime stage ----
FROM docker.io/library/debian:bookworm-slim

# minimal runtime: no compilers, no package managers
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd -r kanban && useradd -r -g kanban -s /usr/sbin/nologin kanban && \
    mkdir -p /data /app/web && chown kanban:kanban /data

WORKDIR /app
COPY --from=builder /kanban /app/kanban
COPY web/ /app/web/

# drop to non-root
USER kanban

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD ["/app/kanban", "-addr", ":0"] || exit 1

ENTRYPOINT ["/app/kanban"]
CMD ["-addr", ":8080", "-db", "/data/kanban.db"]
