# quickproxy (aws-quickwork-2api) — build from source, run on linux/arm64
# Multi-stage: Go builder -> minimal Alpine runtime. No CGO needed
# (modernc.org/sqlite is pure Go), so the binary is fully static.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /quickproxy ./cmd/quickproxy

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /quickproxy /usr/local/bin/quickproxy
COPY config.json ./
# Runtime state (SQLite accounts/tokens) lives in /app/data — mount a
# persistent volume here in Coolify, or logins vanish on every redeploy.
VOLUME ["/app/data"]
EXPOSE 8080
ENTRYPOINT ["quickproxy", "server"]
