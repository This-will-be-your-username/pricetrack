# PriceTrack

A small, self-hosted price tracker. Paste a product link from a major merchant (Amazon, Walmart, Target, Best Buy, eBay, Etsy, Newegg…), optionally set a target price, and PriceTrack checks it in the background, charting changes and flagging drops.

- Single static binary, no CGO, embedded UI and SQLite (pure Go)
- Linux, macOS, Windows (amd64 + arm64) and Docker (amd64 + arm64)
- Extracts prices from schema.org JSON-LD, OpenGraph/product meta tags and common storefront markup
- Pick your region in Settings: only retailers serving it are offered, and prices default to its currency
- "Find stores" searches the web for retailers of a niche product in your region and checks each for a readable price

> Some merchants aggressively block automated requests. When a check fails the card shows why and keeps its history.

## Run

```bash
go run .                    # http://localhost:8080
```

Or grab a binary from the Releases page, or use Docker:

```bash
docker run -d -p 8080:8080 -v pricetrack-data:/data ghcr.io/this-will-be-your-username/pricetrack:latest
```

`docker compose up -d` also works with the included `docker-compose.yml`.

## Configuration (environment variables)

| Variable           | Default | Description                                              |
|--------------------|---------|----------------------------------------------------------|
| `ADDR`             | `:8080` | Listen address                                           |
| `DATA_DIR`         | `data`  | Directory for the SQLite database                        |
| `REFRESH_INTERVAL` | `6h`    | How often all products are re-checked (min `1m`)        |
| `AUTH_USER` / `AUTH_PASS` | unset | Enable HTTP basic auth                            |
| `ALLOW_PRIVATE`    | unset   | Set to `1` to allow fetching private/loopback addresses  |
| `SEARCH_API_KEY`   | unset   | Brave Search API key for "Find stores" (otherwise keyless DuckDuckGo is used) |

`GET /api/products` returns all products and their price history as JSON.

## Releases

Pushing a tag like `v1.0.0` builds binaries for all platforms, publishes a GitHub release, and pushes a multi-arch image to `ghcr.io`. Pushes to `main` publish the `edge` image.

```bash
git tag v1.0.0 && git push origin v1.0.0
```

## Development

```bash
go test ./...
```
