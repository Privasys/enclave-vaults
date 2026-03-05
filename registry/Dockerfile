# syntax=docker/dockerfile:1

# ── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /registry .

# ── Runtime stage ────────────────────────────────────────────────────────────
FROM scratch

COPY --from=build /registry /registry

EXPOSE 8080

ENTRYPOINT ["/registry"]
