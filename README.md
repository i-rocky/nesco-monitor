# nesco-monitor

Standalone Go program that polls a single NESCO prepaid electricity account
**once per day**, stores balance and recharge history in SQLite, renders a
monthly spending chart, and posts it to a Discord channel via webhook. Sends a
separate `@here` warning if the balance drops below a configurable threshold.

Self-contained: its own `go.mod`, no shared imports, ~14 MB scratch Docker image.

## How it works

NESCO's customer panel refreshes the balance once per day at **00:00
Asia/Dhaka**, but for the first hour or so it sometimes still serves the
*previous* day's cached value. So the monitor polls once a day at **05:00
Asia/Dhaka** — five hours later, after the cache has invalidated — plus once on
startup.

Each poll scrapes both the current balance **and** the full recharge history
from the same panel response, and persists anything new.

Each Discord post contains:

- A main embed with **balance**, **account**, and the change since the previous
  reading. If a recharge was purchased but NESCO hasn't reflected it in the
  balance yet, it also shows the effective balance (current + pending credit).
- A monthly **spending line chart** (per-day consumption for the current month,
  with a dashed average line).
- A red `LOW BALANCE WARNING` embed and an `@here` ping if balance < threshold.

### Accurate spending across recharges and cache gaps

Spending is derived from the day-to-day balance drop, which naively breaks
whenever you recharge (the balance jumps up) or NESCO serves a stale value.
This monitor corrects for both:

- **Recharge-aware.** The energy credited by each recharge (the panel's
  `purchaseamount`) is added back over the interval it lands in, so a recharge
  day shows real consumption instead of a gap or a negative spike.
- **Cache-stale handling.** A reading identical to the previous one is treated
  as a cached re-serve and dropped, so it never inflates the chart.
- **Span distribution.** Consumption between two readings is spread evenly
  across the days they cover (up to a week), so stale/missing days fill in as a
  smooth line rather than leaving holes or a catch-up spike.

## Run with Docker (recommended)

A multi-arch image is published as `wpkpda/nesco-monitor:latest`
(`linux/amd64` and `linux/arm64`).

```sh
docker run -d \
  --name nesco-monitor \
  --restart unless-stopped \
  --env-file .env \
  -e DB_PATH=/data/nesco.db \
  -v nesco-data:/data \
  wpkpda/nesco-monitor:latest
```

`-e DB_PATH=/data/nesco.db` puts the SQLite file in the mounted volume so it
survives container recreation.

## Build the image yourself

Multi-stage build → ~14 MB scratch image. CGO-built `mattn/go-sqlite3`,
statically linked against musl so the binary has no libc dependency.

### Multi-arch (amd64 + arm64) with buildx

```sh
# One-time: docker-container builder + arm64 emulation
docker buildx create --name multiarch --driver docker-container --bootstrap --use
docker run --privileged --rm tonistiigi/binfmt --install arm64

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag your-user/nesco-monitor:latest \
  --push \
  .
```

ARM64 compilation runs under QEMU emulation (~5 min) vs ~40s native for amd64.

### Single-arch (host architecture)

```sh
docker build -t nesco-monitor:latest .
```

## Run locally (development)

Requires Go 1.26+ and a C toolchain (gcc/clang) for the sqlite driver.

```sh
cp .env.example .env
# edit .env: set NESCO_ACCOUNT_NO and DISCORD_WEBHOOK_URL
go build
./nesco-monitor
```

### Environment variables

| Var                     | Required | Default                            | Notes |
|-------------------------|----------|------------------------------------|-------|
| `NESCO_ACCOUNT_NO`      | yes      | —                                  | Numeric customer number, e.g. `12345678` |
| `DISCORD_WEBHOOK_URL`   | yes      | —                                  | Server Settings → Integrations → Webhooks → Copy URL |
| `LOW_BALANCE_THRESHOLD` | no       | `500`                              | BDT. Below this, warning embed + `@here` ping. |
| `DB_PATH`               | no       | `./nesco.db`                       | SQLite file. Set to `/data/nesco.db` in Docker. |
| `NESCO_BASE_URL`        | no       | `https://customer.nesco.gov.bd`    | Override only for testing. |
| `HTTP_TIMEOUT`          | no       | `15s`                              | Per-request timeout for both NESCO and Discord. |

The 05:00 Asia/Dhaka poll time and the monthly chart window are intentionally
**not** configurable — they're the defining behavior of the program.

## Files

| File                     | Role |
|--------------------------|------|
| `main.go`                | Wiring + daily-tick loop + signal handling |
| `config.go`              | `.env` + environment variable loading |
| `client.go`              | NESCO HTTP scraper (language → CSRF → POST) + balance/recharge parsing |
| `store.go`               | SQLite (CGO mattn driver) — balance history + recharges |
| `chart.go`               | Monthly spending chart: recharge-aware, span-distributed |
| `discord.go`             | Webhook multipart POST with embeds + chart attachment |
| `scheduler.go`           | `NextTick` (next 05:00 Asia/Dhaka) |
| `*_test.go`              | Parser, store, scheduler, chart, and dedup unit tests |
| `Dockerfile`             | Multi-stage Alpine build → scratch runtime |

## Notes & limitations

- **Single account.** The binary reads one account from env; the schema is
  keyed by `account_no`, so multi-account is a small change.
- **Outbound only.** Discord webhooks can't receive commands; on-demand queries
  would need a bot identity.
- **Warning frequency.** Fires on every daily tick while below threshold, not
  just on the transition.
- **Static linking.** The Docker binary is statically linked against musl
  (`-linkmode external -extldflags -static`, `osusergo,netgo`) so it runs from
  `FROM scratch`.

## Testing

```sh
go test ./...
```

Covers recharge-history parsing, SQLite upsert/idempotency, `NextTick` boundary
and timezone conditions, the recharge-aware span-distribution spending math
(validated against real production data), and duplicate-read skipping.
`chart_sample_test.go` is a manual-inspection harness that writes sample PNGs to
`./samples/`.
