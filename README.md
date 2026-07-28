# zpool proxy

`zpool proxy` forwards local miners to zpool stratum and provides a LAN-only dashboard that proxies zpool WalletEX data.

## What this project does

- Accepts miner connections on local stratum port.
- Forwards shares to zpool upstream stratum.
- Optionally rewrites `mining.authorize` username/password before forwarding.
- Exposes `/api/zpool/walletex` for wallet stats and serves a lightweight dashboard UI.
- Can send `ntfy` notification when wallet `totalpaid` increases.

## Configuration

Copy `.env.example` to `.env` and update values:

```bash
cp .env.example .env
```

Core variables:

- `STRATUM_PORT`, `WEB_PORT`
- `ZPOOL_STRATUM_HOST`, `ZPOOL_STRATUM_PORT`
- `ZPOOL_STRATUM_USERNAME`, `ZPOOL_STRATUM_PASSWORD`
- `ZPOOL_API_BASE_URL`, `ZPOOL_WALLET_ADDRESS`

Optional:

- `DASHBOARD_USERNAME`, `DASHBOARD_PASSWORD` for dashboard basic auth
- `ZPOOL_NOTIFY_PAYOUT`, `ZPOOL_POLL_SECONDS` for payout polling
- `NTFY_SERVER`, `NTFY_TOPIC`, `NTFY_USER`, `NTFY_PASSWORD` for notifications

## Run locally

```bash
go run .
```

or build:

```bash
go build -o zpool-proxy .
./zpool-proxy
```

## Docker

```bash
docker compose up -d --build
docker compose logs -f zpool-proxy
docker compose down
```

## License

MIT
