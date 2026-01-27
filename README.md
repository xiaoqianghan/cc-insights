# CC-Insights

**Claude Code Usage Analytics & Local Metrics Storage**

Collect and analyze your Claude Code usage metrics locally, while optionally forwarding to your company's monitoring system (e.g., Jellyfish).

## Why?

没有度量就没有改进。CC-Insights 帮助你：

- **量化使用成本** - 知道每天/每周花了多少钱，避免账单惊喜
- **发现效率问题** - Cache 命中率低？可能是 prompt 策略需要优化
- **识别使用模式** - 哪些任务消耗最多？值得改进工作流吗？
- **数据驱动决策** - Opus vs Sonnet vs Haiku，哪个性价比更高？
- **本地数据所有权** - 你的使用数据，你自己掌控

### 你可能发现的问题

| 现象 | 可能的原因 | 改进方向 |
|------|-----------|----------|
| Cache 命中率 < 90% | 频繁切换项目/上下文 | 集中处理同类任务 |
| Output tokens 异常高 | 生成了大量重复代码 | 更精确的 prompt |
| 单次 session 成本过高 | 上下文过长未及时清理 | 适时开启新 session |
| Haiku 调用为 0 | 没有利用轻量模型 | 简单任务切换 Haiku |

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
- Python 3 (with built-in `sqlite3` module)
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
============================================================
  Claude Code Usage - Today
============================================================
  Total Requests:      42
  Input Tokens:        85,234
  Output Tokens:       32,891
  Cache Read Tokens:   3,456,789
  Cache Create Tokens: 312,456
  Total Tokens:        3,887,370
  Est. Cost:           $15.82
  Cache Hit Rate:      97.6%

  Daily Breakdown:
  --------------------------------------------------------
  2025-01-15  |     42 req  |     3,887,370 tokens
============================================================
```

## Insights You Can Gain

### Currently Available
- **Cost tracking** - Daily/weekly/monthly spend with estimated costs
- **Token breakdown** - Input, output, cache read, cache creation tokens
- **Cache hit rate** - Measure prompt caching efficiency
- **Request counts** - Track API call volume over time

### Planned Features
- Cost breakdown by model (Opus vs Haiku vs Sonnet)
- Peak usage hours analysis
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
