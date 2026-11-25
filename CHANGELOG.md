# v0.5.0

## Features

- Import HTTP and SOCKS5 proxies in bulk through the WebUI and store them in SQLite, without a proxy-list file.
- Bind proxies to individual accounts, choose a proxy for OAuth login, inherit a global default, or explicitly connect directly.
- Route login, token refresh, user information, official usage, upstream WebSockets, and attachment operations consistently.
- Keep proxy credentials out of management API responses and protect proxies referenced by accounts or default settings from deletion.
- Document the opt-in, process-scoped TLS handshake compatibility workaround from Issue #10 while preserving default TLS behavior and certificate verification.

## Distribution

- Release native binaries for Windows, Linux, and macOS on amd64 and arm64, with embedded version and commit metadata.
- Publish Linux amd64 / arm64 container images to `ghcr.io/jasonxu114514/aws-quickwork-2api` under `v0.5.0`, `0.5.0`, `0.5`, and `latest`.
- Default Compose to the versioned published image; retain local builds, non-root execution, a read-only root filesystem, and persistent data.
- Fix the Docker build-context allowlist to include outbound routing and add native multi-architecture container builds to CI.
- Publish `SHA256SUMS` for all binaries and `container-image.txt`, which records the immutable multi-platform image reference.
- Validate release tags, pin official Actions to immutable commits, and publish the release only after all binary and container builds succeed.

## Upgrade Notes

- Back up the configuration and SQLite database before upgrading. Existing account databases are migrated automatically; proxy credentials are stored unencrypted, just like account credentials.
- Proxy imports and account bindings apply to subsequent outbound requests. Global `proxy_id` changes require a restart.
- Browser navigation uses the browser's network configuration, independently of the server-side proxy selected for OAuth.
- Only `http://` and `socks5://` proxy protocols are supported in this release.
- Smoke tests and live upstream/proxy checks are intentionally not performed. Validation uses compilation, static analysis, and container image builds without running the application.
