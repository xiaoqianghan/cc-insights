# CC-Insights

**Claude Code Usage Analytics & Local Metrics Storage**

A single Go binary (`cci`) that sits between Claude Code and your company's
monitoring system. It transparently forwards OTEL usage metrics upstream (e.g.
Jellyfish) while storing a local copy in SQLite for cost analysis — so you can
see exactly what you spend without touching the corporate pipeline.

## Why?

You can't improve what you don't measure. CC-Insights helps you:

- **Quantify usage costs** - Know your daily/weekly spend, avoid billing surprises
- **Spot efficiency issues** - Low cache hit rate? Your prompt strategy might need tuning
- **Identify usage patterns** - Which tasks consume the most? Is it worth optimizing your workflow?
- **Make data-driven decisions** - Opus vs Sonnet vs Haiku: which gives you the best ROI?
- **Own your data locally** - Your usage data stays under your control

### Problems You Might Discover

| Symptom | Possible Cause | Action |
|---------|---------------|--------|
| Cache hit rate < 90% | Frequent project/context switching | Batch similar tasks together |
| Unusually high output tokens | Generating lots of repetitive code | Write more precise prompts |
| Single session cost too high | Context grew too long without cleanup | Start a new session sooner |
| Zero Haiku calls | Not leveraging lightweight models | Switch to Haiku for simple tasks |

## Architecture

```
OTLP client (Claude Code, or any exporter emitting claude_code.* metrics)
        │  POST /v1/metrics
        ▼
   cci serve  (:4318)
        ├─ parse OTEL → store to SQLite   (synchronous, before ACK)
        ├─ return 200                      (client considers it delivered)
        └─ forward raw payload → upstream  (background goroutine, e.g. Jellyfish)
```

- **Transparent forwarding**: the exact bytes received are forwarded upstream.
  Even a payload `cci` can't parse is still forwarded, so nothing is lost.
- **Local-first storage**: metrics are parsed and written to SQLite *before*
  the request is acknowledged, so your local copy is durable.
- **Single binary**: no nginx, no Vector, no Python. Just `cci`.

> Replaces the legacy nginx + Vector + Python stack. Migrating? See
> [`docs/MIGRATION_GUIDE.md`](docs/MIGRATION_GUIDE.md).

## Quick Start

### Prerequisites

- Go 1.22+ (to build)
- macOS or Linux
- An OTLP metrics source (Claude Code, or any client exporting the
  `claude_code.*` metric schema)

### Install

```bash
git clone https://github.com/xiaoqianghan/cc-insights.git
cd cc-insights
make install          # builds and installs to ~/.local/bin/cci (no sudo)
```

Ensure `~/.local/bin` is on your `PATH`.

### Configure

Copy the example config and set your upstream endpoint:

```bash
mkdir -p ~/.claude/cc-insights
cp config.example.toml ~/.claude/cc-insights/config.toml
$EDITOR ~/.claude/cc-insights/config.toml
```

At minimum, set `[upstream].url` (leave empty for local-only mode). See
[Configuration](#configuration) below.

### Point your OTLP client at cci

cci listens on `127.0.0.1:4318`. For **Claude Code**, add to
`~/.claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_METRICS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://127.0.0.1:4318/v1/metrics"
  }
}
```

Any other OTLP client works too, as long as it POSTs `claude_code.token.usage`
metrics (OTLP `http/json`) to that endpoint — authentication headers for the
real backend belong in cci's `[upstream.headers]`, not in the client.

### Start the proxy

```bash
cci serve -d      # start in the background
cci status        # confirm it's running
```

The daemon auto-stops after the configured idle timeout (default 15m). Re-run
`cci serve -d` to bring it back — or wire it into an on-demand hook (see
[Keeping cci running](#keeping-cci-running)).

## Usage

```bash
cci serve            # run in foreground (debug)
cci serve -d         # run as a background daemon
cci stop             # graceful shutdown (drains in-flight upstream forwards)
cci status           # is it running? upstream failures? total records?
cci stats            # today's usage (default)
cci stats week       # last 7 days
cci stats month      # last 30 days
cci stats year       # last 365 days
cci config           # print the effective configuration (JSON)
cci migrate          # import legacy JSONL from ~/.claude/cc-insights/raw/
cci uninstall-legacy # remove old nginx/Vector configs (preserves data)
```

### Example Output

`cci stats week`:

```
─── Claude Code Usage (Jul 10 - Jul 16) ───────────────────

  Requests:  1,156        Cost: $312.40
  Tokens:    48,204,915   Cache Hit: 98.7%
  Trend:     ▲ +12.0% vs prev week

  Model Breakdown:
  MODEL            REQUESTS      TOKENS       COST  SHARE
  opus-4-8              892   41,203,882   $268.10  85.8% ████████▌
  sonnet-4-6            201    6,340,112    $38.20  12.2% █▏
  haiku-4-5             63       660,921     $6.10   2.0% ▏

  Peak Hours: 15:00-16:00 (142 req), 10:00-11:00 (118 req)

  Daily:
  DATE           REQ      COST TREND
  Jul 16          59   $10.58   ▼ -20%
  Jul 15         326   $92.40   ▲ +8%
  ...
```

## Configuration

Config lives at `~/.claude/cc-insights/config.toml` (override with the
`CCI_CONFIG` environment variable). All keys are optional; defaults are shown.

```toml
[proxy]
listen = "127.0.0.1:4318"   # address the OTEL proxy listens on
idle_timeout = "15m"        # auto-shutdown after this much inactivity

[upstream]
url = ""                    # forward here; empty = local-only (no forwarding)
timeout = "10s"             # per-request timeout for upstream POSTs

[upstream.headers]          # extra headers sent with every forwarded request
# Authorization = "Bearer YOUR_TOKEN"
# Host = "app.jellyfish.co"

[storage]
db_path = "~/.claude/cc-insights/metrics.db"
retention_days = 1825       # rows older than this are pruned on `cci stats`

# Per-model pricing, USD per million tokens. Cost is computed at query time,
# so editing these re-prices historical data retroactively.
# Model keys are the SHORT normalized form (see "Model normalization").
# For AWS Bedrock Regional endpoints, apply a 10% premium.
[pricing.opus-4-8]
input = 5.0
output = 25.0
cache_read = 0.50
cache_write = 6.25
```

See [`config.example.toml`](config.example.toml) for the full pricing table.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CCI_CONFIG` | `~/.claude/cc-insights/config.toml` | Config file location |

## How It Works

### Request handling

Every `POST /v1/metrics` is handled synchronously up to the ACK:

1. Read the body and reset the idle timer.
2. Parse the OTEL payload. On parse error, still return `200` and forward the
   raw body upstream (non-metric payloads are never dropped).
3. Store parsed token records to SQLite.
4. Return `200`.
5. Forward the raw body upstream in a background goroutine (tracked by a
   `WaitGroup` so `cci stop` drains in-flight forwards before exiting).

Upstream failures are counted in `proxy_stats.upstream_failures` and surfaced by
`cci status`; they never affect local storage or the client's `200`.

### What gets stored

Only `claude_code.token.usage` data points are persisted, keyed by timestamp and
model, with `input` / `output` / `cacheRead` / `cacheCreation` token counts. The
`claude_code.cost.usage` and `claude_code.session.count` metrics are forwarded
upstream but **not** stored — cost is recomputed locally from the pricing table
at query time, so it always reflects your current `[pricing.*]` config.

### Model normalization

Raw model identifiers are normalized to a short canonical form before storage:

```
us.anthropic.claude-opus-4-8              → opus-4-8
us.anthropic.claude-sonnet-4-6-...-v1:0   → sonnet-4-6
claude-haiku-4-5-20251001-v1:0            → haiku-4-5
```

Recognized families: `opus`, `sonnet`, `haiku`, `fable`, `mythos`. Unrecognized
names are preserved as-is (no silent fallback). Pricing keys must match the
normalized short form; models seen without a matching `[pricing.*]` entry show
`$0.00` and are listed as missing by `cci stats`.

## Data Storage

```
~/.claude/cc-insights/
├── metrics.db       # SQLite: metrics + proxy_stats tables
├── config.toml      # your configuration
└── cci.pid          # daemon PID + executable path
```

The `metrics` table is the source of truth; `cci stats` aggregates it and
computes cost on the fly. WAL mode is enabled, so reads (stats/status) and the
daemon's writes coexist safely.

## Keeping cci running

The daemon shuts itself down after `idle_timeout` and there is no bundled
service manager. To make it start on demand, add a shell hook that launches it
if the port is free:

```bash
# ~/.zshrc — start cci if nothing is listening on 4318
if ! lsof -i :4318 -sTCP:LISTEN &>/dev/null; then
  cci serve -d &>/dev/null
fi
```

## Troubleshooting

**Proxy not running / no data collected**

```bash
cci status                                   # running? record count?
cat ~/.claude/settings.json | grep OTEL      # client pointed at :4318?
cci serve -d                                 # (re)start it
```

**Upstream failures climbing** (`cci status`)

- Verify `[upstream.headers].Authorization` is valid (expired token is the usual cause).
- Check `[upstream].url` — some backends reject the `/v1/metrics` suffix.
- Local storage is unaffected; only forwarding failed.

**`cci stats` shows a model at $0.00 / "missing pricing"**

- Add a `[pricing.<short-model>]` entry using the normalized short name
  (e.g. `opus-4-8`, not the full Bedrock ID).

## Development

```bash
make build    # build to ./build/cci
make test     # go test ./...
make lint     # golangci-lint
make fmt      # goimports + gofmt
make check    # fmt + lint + test
```

Layout:

```
cmd/cci/            # CLI entrypoint (cobra commands)
internal/proxy/     # HTTP server, upstream forwarding, daemon lifecycle
internal/otel/      # OTEL JSON parsing + model normalization
internal/storage/   # SQLite schema, inserts, stats counters
internal/stats/     # query aggregation + terminal rendering
internal/config/    # TOML config loading + defaults
```

## License

MIT

## Contributing

Issues and PRs welcome!
