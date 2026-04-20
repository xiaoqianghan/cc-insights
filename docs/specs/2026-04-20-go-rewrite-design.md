# CC-Insights Go Rewrite Design

**Date**: 2026-04-20
**Status**: Approved
**Goal**: Replace Nginx + Vector + Python stack with a single Go binary that maintains transparent proxy behavior while drastically reducing operational complexity.

---

## Problem Statement

Current architecture uses three separate processes (Nginx, Vector, Python/uv) with dual storage (JSONL + SQLite), template-based configuration, and macOS-only deployment. This creates unnecessary operational burden for what is fundamentally a simple task: intercept OTEL metrics, forward to upstream, store locally, and analyze.

## Solution Overview

A single Go binary (`cci`) that serves as both the transparent proxy and the analytics CLI. It listens on the same port (4318), forwards to Jellyfish, writes directly to SQLite, and provides stats via subcommands.

---

## Architecture

```
Claude Code → cci serve (:4318)
               ├─ goroutine: forward payload → Jellyfish (async, log failures)
               └─ goroutine: parse OTEL → write SQLite (async)
               └─ immediately return 200 to Claude Code

cci stats [today|week|month|year] → read SQLite → terminal output
cci status → check proxy state, failure count, data stats
```

### Key Properties

- **Non-blocking**: Handler returns 200 immediately, processing is async
- **Single process**: One binary handles proxy + CLI + storage
- **Auto-lifecycle**: Starts on demand, exits after idle timeout
- **Direct storage**: OTEL → SQLite, no intermediate JSONL layer

---

## CLI Commands

```bash
# Proxy management
cci serve                     # Foreground (debug)
cci serve -d                  # Daemon mode
cci serve --idle-timeout 15m  # Custom timeout (default 15m)
cci stop                      # Graceful shutdown

# Analytics
cci stats                     # Today
cci stats week                # This week
cci stats month               # This month
cci stats year                # This year

# Operations
cci status                    # Proxy running? Port? Failures? Record count?
cci config                    # Show current config

# Migration (one-time)
cci migrate                   # Import existing JSONL → new SQLite
cci uninstall-legacy          # Remove old nginx/vector configs
```

---

## Lifecycle Management

### Auto-Start

Via `.zshrc` or Claude Code hook:

```bash
if ! lsof -i :4318 -sTCP:LISTEN &>/dev/null; then
    cci serve -d
fi
```

Additionally, `cci stats` and other subcommands auto-start the proxy if not running.

### Idle Timeout

- Each incoming request resets a 15-minute timer
- On timeout expiry: log, clean PID file, graceful exit
- Multiple tmux sessions keep the timer alive as long as any session is active

### Multi-Instance Protection

- PID file at `~/.claude/cc-insights/cci.pid`
- On startup: check PID file, verify process alive, exit if already running

---

## Data Model

### SQLite Schema

```sql
CREATE TABLE metrics (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp             TEXT NOT NULL,
    date                  TEXT NOT NULL,
    hour                  INTEGER NOT NULL,
    model                 TEXT NOT NULL,
    input_tokens          INTEGER DEFAULT 0,
    output_tokens         INTEGER DEFAULT 0,
    cache_read_tokens     INTEGER DEFAULT 0,
    cache_creation_tokens INTEGER DEFAULT 0,
    created_at            TEXT DEFAULT (datetime('now'))
);

CREATE INDEX idx_metrics_date ON metrics(date);
CREATE INDEX idx_metrics_model ON metrics(model);
CREATE INDEX idx_metrics_date_model ON metrics(date, model);

CREATE TABLE proxy_stats (
    key   TEXT PRIMARY KEY,
    value TEXT
);
```

### SQLite Configuration

```sql
PRAGMA journal_mode=WAL;       -- Concurrent read/write support
PRAGMA busy_timeout=5000;      -- Wait on write conflicts
```

### Data Retention

- 5 years (1825 days)
- Checked on proxy startup, deletes expired records

---

## Proxy Logic

### Request Handler

1. Read request body
2. Return HTTP 200 immediately
3. Reset idle timer
4. Launch goroutine: parse OTEL, write SQLite
5. Launch goroutine: forward to upstream

### OTEL Parsing

Path: `payload.resourceMetrics[].scopeMetrics[].metrics[]`

- Filter: only `name == "claude_code.token.usage"`
- Extract attributes: `model`, `type` (input/output/cacheRead/cacheCreation)
- Extract value: `asInt` or `asDouble`
- Normalize model name: `us.anthropic.claude-opus-4-6-v1:0` → `opus-4-6`

### Model Name Normalization

- Extract family: opus, sonnet, haiku
- Extract version digits after family name
- Unrecognized family: preserve raw name (no silent fallback)

### Upstream Forwarding

- POST original body to configured upstream URL
- Pass through configured headers (Authorization, Content-Type)
- Timeout: 10s
- On failure: increment `proxy_stats["upstream_failures"]`, log to stderr

### Error Handling

| Scenario | Behavior |
|----------|----------|
| OTEL parse failure (non-token data) | Forward to upstream anyway, skip local storage |
| Upstream unreachable | Write locally, increment failure counter, log |
| SQLite write failure | Log to stderr, don't affect upstream forwarding |
| Concurrent requests | Go HTTP server + SQLite WAL handles naturally |

---

## Configuration

File: `~/.claude/cc-insights/config.toml`

```toml
[proxy]
listen = "127.0.0.1:4318"
idle_timeout = "15m"

[upstream]
url = "https://jellyfish.example.com/v1/metrics"
timeout = "10s"

[upstream.headers]
Authorization = "Bearer xxx"

[storage]
db_path = "~/.claude/cc-insights/metrics.db"
retention_days = 1825

[pricing.opus-4-6]
input = 5.0
output = 25.0
cache_read = 0.50
cache_write = 6.25

[pricing.opus-4-5]
input = 5.0
output = 25.0
cache_read = 0.50
cache_write = 6.25

[pricing.opus-4-1]
input = 15.0
output = 75.0
cache_read = 1.50
cache_write = 18.75

[pricing.sonnet-4-6]
input = 3.0
output = 15.0
cache_read = 0.30
cache_write = 3.75

[pricing.sonnet-4-5]
input = 3.0
output = 15.0
cache_read = 0.30
cache_write = 3.75

[pricing.sonnet-4-0]
input = 3.0
output = 15.0
cache_read = 0.30
cache_write = 3.75

[pricing.haiku-4-5]
input = 1.0
output = 5.0
cache_read = 0.10
cache_write = 1.25

[pricing.haiku-3-5]
input = 0.80
output = 4.0
cache_read = 0.08
cache_write = 1.0
```

Pricing in config solves the "hardcoded pricing" issue from the adversarial review. Users update prices by editing config, no recompilation needed.

---

## Stats Output

Simple text + ANSI colors:

```
─── Claude Code Usage (Apr 14 - Apr 20) ───────────────────

  Requests:  87          Cost: $12.34
  Tokens:    1,245,000   Cache Hit: 72.3%

  Model Breakdown:
  MODEL        REQUESTS  TOKENS      COST     SHARE
  opus-4-6     34        890,000     $9.80    79.4% ████████
  sonnet-4-6   41        320,000     $2.15    17.4% ██
  haiku-4-5    12        35,000      $0.39     3.2% ▏

  Peak Hours: 10:00-11:00 (18 req), 14:00-15:00 (15 req)

  Daily:
  DATE        REQ   COST    vs avg
  Apr 20      14    $1.89   ▲ +12%
  Apr 19      11    $1.45   ▼ -8%

  Upstream: 87 forwarded, 0 failed
```

Start minimal, add charmbracelet polish later.

---

## Migration Strategy

### Step 1: Data Migration

```bash
cci migrate
```

- Reads all `~/.claude/cc-insights/raw/metrics-*.jsonl` files
- Parses OTEL, writes to new SQLite schema
- Reports count of migrated records
- Does NOT delete old files (user decides)

### Step 2: Service Switch

```bash
brew services stop nginx
brew services stop vector
cci serve -d
```

Port 4318 unchanged — Claude Code `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` needs no change.

### Step 3: Legacy Cleanup (optional)

```bash
cci uninstall-legacy
```

Removes:
- `/opt/homebrew/etc/nginx/servers/cc-insights.conf`
- `/opt/homebrew/etc/vector/vector.yaml`
- Old CLI symlinks

Preserves:
- `~/.claude/cc-insights/raw/` (user can delete manually)
- `~/.claude/cc-insights/metrics.db` (old one, if different from new)

---

## Project Structure

```
cc-insights/
├── cmd/
│   └── cci/
│       └── main.go              # Entry point, subcommand routing
├── internal/
│   ├── proxy/
│   │   ├── server.go            # HTTP server + request handler
│   │   ├── upstream.go          # Upstream forwarding logic
│   │   └── lifecycle.go         # PID file, idle timeout, daemon
│   ├── otel/
│   │   └── parser.go            # OTEL JSON parsing + model normalization
│   ├── storage/
│   │   ├── db.go                # SQLite init, migrations, retention
│   │   └── writer.go            # INSERT logic
│   ├── stats/
│   │   ├── query.go             # Aggregation queries
│   │   └── render.go            # Terminal output formatting
│   └── config/
│       └── config.go            # TOML config loading
├── config.example.toml
├── go.mod
├── go.sum
├── Makefile                      # build, install, test targets
└── README.md
```

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/mattn/go-sqlite3` | SQLite driver (CGo) |
| `github.com/BurntSushi/toml` | Config file parsing |
| `github.com/spf13/cobra` | CLI subcommand framework |
| Standard library `net/http` | HTTP server + client |
| Standard library `encoding/json` | OTEL JSON parsing |

---

## Comparison: Before vs After

| Dimension | Current (Nginx + Vector + Python) | New (cci Go binary) |
|-----------|-----------------------------------|---------------------|
| Components | 3 processes + 2 configs | 1 binary + 1 config |
| Installation | brew + uv + template substitution | `go install` or download binary |
| Runtime deps | Nginx, Vector, Python, uv, Rich | None (static binary) |
| Storage | JSONL + SQLite (dual) | SQLite only |
| Daemon behavior | Always running | On-demand + idle exit |
| Pricing updates | Edit code, re-run | Edit config.toml |
| Platform | macOS only | Go cross-compile (Linux/macOS) |
| Data consistency | Sync window between JSONL and DB | Immediate (direct write) |
