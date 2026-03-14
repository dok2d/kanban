# ---- Download static assets ----
FROM docker.io/library/debian:bookworm-slim AS assets

RUN apt-get update && \
    apt-get install -y --no-install-recommends curl ca-certificates && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /assets/js /assets/css /assets/fonts

# JavaScript libraries
RUN curl -sL "https://cdn.jsdelivr.net/npm/marked/marked.min.js" -o /assets/js/marked.min.js && \
    curl -sL "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js" -o /assets/js/highlight.min.js

# highlight.js theme
RUN curl -sL "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/atom-one-dark.min.css" -o /assets/css/atom-one-dark.min.css

# Google Fonts — JetBrains Mono (400, 600) + Source Sans 3 (400, 600, 700)
RUN curl -sL "https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;600&family=Source+Sans+3:wght@400;600;700&display=swap" \
      -H "User-Agent: Mozilla/5.0" -o /tmp/fonts.css && \
    grep -oP 'url\(\K[^)]+' /tmp/fonts.css | while read -r url; do \
      fname=$(echo "$url" | sed 's|.*/||'); \
      curl -sL "$url" -o "/assets/fonts/$fname"; \
    done && \
    sed -E 's|url\(https?://[^)]*/(.[^/)]+)\)|url(/static/fonts/\1)|g' /tmp/fonts.css > /assets/css/fonts.css

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

# Copy downloaded static assets
COPY --from=assets /assets/js/ /app/web/static/js/
COPY --from=assets /assets/css/ /app/web/static/css/
COPY --from=assets /assets/fonts/ /app/web/static/fonts/

# drop to non-root
USER kanban

EXPOSE 8080

ENTRYPOINT ["/app/kanban"]
CMD ["-addr", ":8080", "-db", "/data/kanban.db"]
