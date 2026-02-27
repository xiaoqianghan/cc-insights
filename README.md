# CC-Insights

**Claude Code Usage Analytics & Local Metrics Storage**

Collect and analyze your Claude Code usage metrics locally, while optionally forwarding to your company's monitoring system (e.g., Jellyfish).

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
Claude Code → Nginx (4318) → ├── Upstream (transparent proxy)
                             └── Vector (4319) → Local Storage
```

- **Nginx**: Listens on `:4318`, proxies to upstream, mirrors to Vector
- **Vector**: Receives mirrored data, stores to daily JSONL files
- **SQLite**: Aggregated metrics for fast queries

## Quick Start

### Prerequisites

- macOS with Homebrew
- [uv](https://docs.astral.sh/uv/) (installed automatically by the installer)
- Python 3.10+ (managed by uv)
- Claude Code CLI

### Installation

```bash
git clone https://github.com/xiaoqianghan/cc-insights.git
cd cc-insights
./install.sh
```

The installer will:
1. Install nginx and vector via Homebrew
2. Configure the proxy and storage
3. Create the `cci` CLI command
4. Start services

### Configure Claude Code

Add to `~/.claude/settings.json`:

```json
{
  "env": {
    "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://127.0.0.1:4318/v1/metrics"
  }
}
```

If your company requires authentication headers:

```json
{
  "env": {
    "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://127.0.0.1:4318/v1/metrics",
    "OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer your_token_here"
  }
}
```

## Usage

### CLI Commands

```bash
# Use scripts directly
./scripts/ctl.sh status          # Check service status
./scripts/ctl.sh stats           # Today's usage
./scripts/ctl.sh stats week      # This week's usage
./scripts/ctl.sh stats month     # This month's usage
./scripts/ctl.sh test            # Send test metric
./scripts/ctl.sh logs            # View Vector logs
./scripts/ctl.sh start/stop      # Control services

# Or install global command (optional)
sudo ln -sf $(pwd)/scripts/ctl.sh /usr/local/bin/cci
cci stats   # Then use cci anywhere
```

### Example Output

```
╭──────────────────────────────╮
│  CC-Insights · This Week     │
╰──────────────────────────────╯

  Requests: 156  ▲ +12%    Cost: $45.32  ▼ -5%    Cache Hit: 96.1%

╭──────────────┬──────┬────────┬────────┬────────────┬────────┬───────────────╮
│ Model        │ Reqs │ Input  │ Output │ Cache Read │ Cost   │ Share         │
├──────────────┼──────┼────────┼────────┼────────────┼────────┼───────────────┤
│ opus-4-5     │   98 │ 1.2M   │ 320K   │ 8.5M       │ $38.50 │ █████████░░░  │
│ sonnet-4-5   │   45 │ 400K   │ 180K   │ 2.1M       │  $5.82 │ ██░░░░░░░░░░  │
│ haiku-3-5    │   13 │ 80K    │ 40K    │ 500K       │  $1.00 │ ░░░░░░░░░░░░  │
╰──────────────┴──────┴────────┴────────┴────────────┴────────┴───────────────╯

  Peak Hours
  09 ██████████████ 23
  10 ████████████████████ 31
  14 ██████████████████ 28
  15 ████████████████████████████ 42
```

## Insights You Can Gain

- **Per-model cost breakdown** - See exactly how much each model (Opus, Sonnet, Haiku) costs you
- **Dynamic pricing** - Accurate cost estimates using model-specific token pricing
- **Peak hours analysis** - Visualize your usage patterns throughout the day
- **Trend comparison** - Week-over-week and period-over-period changes with trend indicators
- **Cache hit rate** - Measure prompt caching efficiency
- **Rich TUI output** - Beautiful terminal reports with tables and bar charts

### Planned Features
- Session-level insights
- Budget alerts and forecasting

## Data Storage

```
~/.claude/cc-insights/
├── raw/                    # Raw JSONL metrics (daily files)
│   ├── metrics-2026-01-20.jsonl
│   └── metrics-2026-01-21.jsonl
├── metrics.db              # SQLite for fast queries
└── vector-data/            # Vector internal state
```

### Data Format

Each line in the JSONL files is an OTEL metrics payload:

```json
{
  "resourceMetrics": [{
    "resource": {
      "attributes": [
        {"key": "user.email", "value": {"stringValue": "you@company.com"}},
        {"key": "service.name", "value": {"stringValue": "claude-code"}}
      ]
    },
    "scopeMetrics": [{
      "metrics": [
        {"name": "claude_code.cost.usage", "unit": "USD", ...},
        {"name": "claude_code.token.usage", "unit": "tokens", ...}
      ]
    }]
  }]
}
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CC_INSIGHTS_DATA_DIR` | `~/.claude/cc-insights` | Data storage location |

### Config Files

| File | Location |
|------|----------|
| Nginx config | `/opt/homebrew/etc/nginx/servers/cc-insights.conf` |
| Vector config | `/opt/homebrew/etc/vector/vector.yaml` |

## Troubleshooting

### Services not running

```bash
./scripts/ctl.sh status       # Check status
brew services restart nginx   # Restart nginx
brew services restart vector  # Restart vector
```

### No data being collected

1. Verify Claude Code settings:
   ```bash
   cat ~/.claude/settings.json | grep OTEL
   ```

2. Test the endpoint:
   ```bash
   ./scripts/ctl.sh test
   ```

3. Check logs:
   ```bash
   ./scripts/ctl.sh logs
   tail -f /opt/homebrew/var/log/nginx/error.log
   ```

### Upstream 403 errors

- Verify your Authorization header is correct
- Check the upstream URL format (some services don't want `/v1/metrics` suffix)

## Uninstall

```bash
./uninstall.sh
```

This removes configs and the CLI command but preserves your data.

## How It Works

### Nginx Mirror

The key mechanism is nginx's `mirror` directive:

```nginx
mirror /mirror;
mirror_request_body on;

location / {
    proxy_pass $upstream;  # Main request
}

location = /mirror {
    internal;
    proxy_pass http://vector_local;  # Copy of request
}
```

Every request is:
1. Proxied to upstream (response returned to client)
2. Mirrored to Vector (response discarded)

This is non-blocking and doesn't affect latency.

## License

MIT

## Contributing

Issues and PRs welcome!
