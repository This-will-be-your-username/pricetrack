# PriceTrack

A small, self-hosted tool that finds the cheapest retailer for a product. Add a product name, model number or link; PriceTrack searches the web for stores that sell it in your region, compares their prices, and keeps watching for the best deal.

- Single static binary, no CGO, embedded UI and SQLite (pure Go)
- Linux, macOS, Windows (amd64 + arm64) and Docker (amd64 + arm64)
- Extracts prices from schema.org JSON-LD, OpenGraph/product meta tags and common storefront markup
- Pick your region in Settings: searches only cover stores serving it, and prices default to its currency
- Results are checked for relevance (model numbers must match) and for a readable price; add your own retailer links too
- Always shows the genuinely cheapest price, even when conditional, labelled with its caveat (member price, subscribe & save / repeat delivery, business, student) alongside the cheapest standard price

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
| `SEARCH_API_KEY`   | unset   | Brave Search API key for retailer search (otherwise keyless DuckDuckGo is used) |

`GET /api/items` returns every product with its retailers, cheapest price and best-price history as JSON.

## Releases

Pushing a tag like `v1.0.0` builds binaries for all platforms, publishes a GitHub release, and pushes a multi-arch image to `ghcr.io`. Pushes to `main` publish the `edge` image.

```bash
git tag v1.0.0 && git push origin v1.0.0
```

## Development

```bash
go test ./...
```
