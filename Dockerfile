# PocketNAS — multi-stage build (pure Go, CGO-free via modernc.org/sqlite).
FROM golang:1.23 AS build
WORKDIR /src
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.Version=${VERSION} -X pocket-nas/internal/files.Version=${VERSION}" \
      -o /out/pocket-nas ./cmd/pocket-nas

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 1000 pocketnas
COPY --from=build /out/pocket-nas /usr/local/bin/pocket-nas
USER pocketnas
VOLUME /data
EXPOSE 8080
CMD ["pocket-nas", "-root", "/data", "-addr", "0.0.0.0", "-port", "8080"]
