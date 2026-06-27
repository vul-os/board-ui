# Production Vulos board sync server — a single CGO-free static Go binary.
# Mirrors wede's server-side Yjs approach (github.com/reearth/ygo, pure-Go).
# Build context is this directory (server-go/).

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=0 proves the whole tree (incl. ygo) is pure-Go.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/board-server .

FROM alpine:3.20
RUN adduser -D -u 10001 board
COPY --from=build /out/board-server /usr/local/bin/board-server
USER board
ENV BOARD_LISTEN_ADDR=:8080 \
    BOARD_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/board-server"]
