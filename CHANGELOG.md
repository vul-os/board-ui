# Changelog

All notable changes to `@vulos/board-ui` are documented here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html)

---

## [Unreleased]

### Security

- **Go server: DoS caps wired (HIGH)** — `MaxConnections` (1 024), `MaxPeersPerRoom` (64), `MessageRateLimit` (50 msg/s), and `MaxAwarenessBytesPerRoom` (100 MiB) are now configured from env with sane defaults, matching the Node dev server's existing caps. Previously these were left at 0 (unlimited). Env overrides: `BOARD_MAX_CONNS`, `BOARD_MAX_ROOM_CONNS`, `BOARD_MESSAGE_RATE`, `BOARD_MAX_AWARENESS_BYTES`.
- **Go server: `room` claim required (HIGH)** — auth tokens must carry a non-empty `room` claim that matches the joined room. A room-less token previously acted as a master key valid for any room on the server.
- **Go server: `exp` claim required + max-TTL bound (HIGH)** — auth tokens must carry a valid, non-expired `exp` claim. Tokens whose lifetime exceeds `BOARD_MAX_TOKEN_TTL_SECONDS` (default 3 600 s) are also rejected, preventing over-long-lived URL-borne tokens. Matches the Node server's SECURE-mode policy.
- **Go server: auth-OFF forces loopback binding (MED)** — when `BOARD_AUTH_SECRET` is unset the server now forces its listen address to `127.0.0.1:<port>` so an unauthenticated instance is never reachable off the local machine. Set `BOARD_ALLOW_INSECURE=1` to restore the old any-interface behaviour for explicit local-dev setups.
- **Go server: `ro` token claim enforces read-only connections (MED)** — tokens may carry `"ro": true` to admit a peer as read-only. Write messages from such peers are dropped at the server; the peer receives the initial state and all live updates from other peers.
- **Go server: oversized-blob pruning ported from Node (MED)** — files-YMap entries whose `dataURL` exceeds `BOARD_MAX_BLOB_BYTES` (default 10 MiB) are deleted server-side immediately after insertion, replicating to all peers. Mirrors the blob-pruning logic in the Node dev server.

## [0.1.0] — 2026-06-28

### Added

- Initial release of `@vulos/board-ui` — collaborative whiteboard React component.
- Yjs CRDT core with Excalidraw integration; real-time multi-user canvas.
- ESM + CJS dual build via Vite.
- Go sync server (`server-go/`) with auth, persistence, and concurrency hardening.
