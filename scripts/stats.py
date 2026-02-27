#!/usr/bin/env python3
"""
CC-Insights: Claude Code Usage Statistics

Commands:
    cci stats              # Today's summary
    cci stats week         # This week's summary
    cci stats month        # This month's summary
    cci stats sync         # Sync JSON files to SQLite
    cci stats check        # Check upstream forwarding status
    cci stats tail         # Watch for new metrics (live)
"""

import json
import sqlite3
import os
import sys
from datetime import datetime, timedelta
from pathlib import Path
from glob import glob

from rich.console import Console
from rich.table import Table
from rich.panel import Panel
from rich.text import Text
from rich import box

console = Console()

# Data directory (configurable via environment)
DATA_DIR = Path(os.environ.get("CC_INSIGHTS_DATA_DIR", Path.home() / ".claude" / "cc-insights"))
RAW_DIR = DATA_DIR / "raw"
FAILED_DIR = DATA_DIR / "failed"
DB_PATH = DATA_DIR / "metrics.db"

# Multi-model pricing (per 1M tokens)
MODEL_PRICING = {
    "claude-opus-4-5-20250115":       {"input": 15, "output": 75, "cache_read": 1.875, "cache_write": 18.75},
    "claude-sonnet-4-5-20250115":     {"input": 3, "output": 15, "cache_read": 0.30, "cache_write": 3.75},
    "claude-haiku-3-5-20241022":      {"input": 0.80, "output": 4, "cache_read": 0.08, "cache_write": 1},
    # Aliases for partial matches
    "opus":    {"input": 15, "output": 75, "cache_read": 1.875, "cache_write": 18.75},
    "sonnet":  {"input": 3, "output": 15, "cache_read": 0.30, "cache_write": 3.75},
    "haiku":   {"input": 0.80, "output": 4, "cache_read": 0.08, "cache_write": 1},
}
DEFAULT_PRICING = {"input": 15, "output": 75, "cache_read": 1.875, "cache_write": 18.75}


def get_pricing(model: str | None) -> dict:
    """Get pricing for a model, with fuzzy matching."""
    if not model:
        return DEFAULT_PRICING
    # Exact match
    if model in MODEL_PRICING:
        return MODEL_PRICING[model]
    # Fuzzy match by keyword
    model_lower = model.lower()
    for keyword in ("opus", "sonnet", "haiku"):
        if keyword in model_lower:
            return MODEL_PRICING[keyword]
    return DEFAULT_PRICING


def init_db():
    """Initialize SQLite database."""
    conn = sqlite3.connect(DB_PATH)
    conn.execute("""
        CREATE TABLE IF NOT EXISTS metrics (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            timestamp TEXT NOT NULL,
            date TEXT NOT NULL,
            model TEXT,
            input_tokens INTEGER DEFAULT 0,
            output_tokens INTEGER DEFAULT 0,
            cache_read_tokens INTEGER DEFAULT 0,
            cache_creation_tokens INTEGER DEFAULT 0,
            total_tokens INTEGER DEFAULT 0,
            request_count INTEGER DEFAULT 1,
            raw_data TEXT,
            synced_at TEXT DEFAULT CURRENT_TIMESTAMP
        )
    """)
    conn.execute("CREATE INDEX IF NOT EXISTS idx_metrics_date ON metrics(date)")
    conn.execute("CREATE INDEX IF NOT EXISTS idx_metrics_model ON metrics(model)")
    conn.execute("""
        CREATE TABLE IF NOT EXISTS sync_state (
            file_path TEXT PRIMARY KEY,
            last_line INTEGER DEFAULT 0,
            synced_at TEXT DEFAULT CURRENT_TIMESTAMP
        )
    """)
    # Migration: add cache columns if missing
    for col in ["cache_read_tokens", "cache_creation_tokens"]:
        try:
            conn.execute(f"ALTER TABLE metrics ADD COLUMN {col} INTEGER DEFAULT 0")
        except sqlite3.OperationalError:
            pass
    conn.commit()
    return conn


def normalize_model_name(raw: str | None) -> str | None:
    """Normalize model identifiers to short canonical names.

    e.g. 'us.anthropic.claude-opus-4-6-v1' -> 'opus-4-6'
         'global.anthropic.claude-haiku-4-5-20251001-v1:0' -> 'haiku-4-5'
    """
    if not raw:
        return None
    for family in ("opus", "sonnet", "haiku"):
        if family in raw.lower():
            # Extract version digits after family name
            # "claude-opus-4-6-v1" or "claude-opus-4-5-20251101-v1:0"
            parts = raw.replace(".", "-").replace(":", "-").split("-")
            try:
                idx = next(i for i, p in enumerate(parts) if p == family)
                version_parts = []
                for p in parts[idx + 1:]:
                    if p.isdigit() and len(p) <= 2:
                        version_parts.append(p)
                    else:
                        break
                if version_parts:
                    return f"{family}-{'-'.join(version_parts)}"
                return family
            except StopIteration:
                return family
    return raw


def parse_otel_metrics(data: dict, fallback_date: str = None) -> list[dict]:
    """Parse OTEL metric data, returning one record per model."""
    if not isinstance(data, dict):
        return []
    # Skip non-OTEL records (e.g. Vector HTTP server metadata)
    if "resourceMetrics" not in data:
        return []
    # Skip empty/test payloads with no actual metrics
    has_metrics = any(
        scope.get("metrics")
        for rm in data.get("resourceMetrics", [])
        for scope in rm.get("scopeMetrics", [])
    )
    if not has_metrics:
        return []

    try:
        latest_time_nano = 0
        # Accumulate tokens per model
        per_model: dict[str | None, dict] = {}

        for rm in data.get("resourceMetrics", []):
            for scope in rm.get("scopeMetrics", []):
                for metric in scope.get("metrics", []):
                    name = metric.get("name", "")
                    data_points = metric.get("sum", {}).get("dataPoints", [])

                    for dp in data_points:
                        time_nano = int(dp.get("timeUnixNano", 0))
                        if time_nano > latest_time_nano:
                            latest_time_nano = time_nano

                    if name == "claude_code.token.usage":
                        for dp in data_points:
                            value = int(dp.get("asDouble", dp.get("asInt", 0)))
                            attrs = {a["key"]: a.get("value", {}).get("stringValue")
                                     for a in dp.get("attributes", [])}
                            token_type = attrs.get("type", "")
                            model = normalize_model_name(attrs.get("model"))

                            if model not in per_model:
                                per_model[model] = {
                                    "input_tokens": 0, "output_tokens": 0,
                                    "cache_read_tokens": 0, "cache_creation_tokens": 0,
                                }
                            bucket = per_model[model]
                            if token_type == "input":
                                bucket["input_tokens"] += value
                            elif token_type == "output":
                                bucket["output_tokens"] += value
                            elif token_type == "cacheRead":
                                bucket["cache_read_tokens"] += value
                            elif token_type == "cacheCreation":
                                bucket["cache_creation_tokens"] += value

        # Determine timestamp
        if latest_time_nano > 0:
            ts = datetime.fromtimestamp(latest_time_nano / 1_000_000_000)
            timestamp = ts.isoformat()
            date = ts.strftime("%Y-%m-%d")
        elif fallback_date:
            timestamp = f"{fallback_date}T00:00:00"
            date = fallback_date
        else:
            timestamp = datetime.now().isoformat()
            date = datetime.now().strftime("%Y-%m-%d")

        # No token data (e.g. session.count, active_time payloads) — skip
        if not per_model:
            return []

        results = []
        for model, tokens in per_model.items():
            total = tokens["input_tokens"] + tokens["output_tokens"] + \
                    tokens["cache_read_tokens"] + tokens["cache_creation_tokens"]
            results.append({
                "timestamp": timestamp,
                "date": date,
                "model": model,
                "input_tokens": tokens["input_tokens"],
                "output_tokens": tokens["output_tokens"],
                "cache_read_tokens": tokens["cache_read_tokens"],
                "cache_creation_tokens": tokens["cache_creation_tokens"],
                "total_tokens": total,
            })
        return results

    except Exception as e:
        print(f"Warning: Error parsing metric: {e}")
        return []


def sync_json_to_db():
    """Sync JSON files to SQLite database."""
    if not RAW_DIR.exists():
        return

    conn = init_db()
    cursor = conn.cursor()

    json_files = sorted(glob(str(RAW_DIR / "*.jsonl")))
    total_new = 0

    for json_file in json_files:
        filename = Path(json_file).name
        fallback_date = None
        if filename.startswith("metrics-") and filename.endswith(".jsonl"):
            fallback_date = filename[8:-6]

        cursor.execute("SELECT last_line FROM sync_state WHERE file_path = ?", (json_file,))
        row = cursor.fetchone()
        last_line = row[0] if row else 0

        with open(json_file, 'r') as f:
            lines = f.readlines()

        new_lines = lines[last_line:]
        if not new_lines:
            continue

        for line in new_lines:
            try:
                data = json.loads(line.strip())
                records = parse_otel_metrics(data, fallback_date=fallback_date)
                for parsed in records:
                    cursor.execute("""
                        INSERT INTO metrics (timestamp, date, model, input_tokens, output_tokens,
                                             cache_read_tokens, cache_creation_tokens, total_tokens, raw_data)
                        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """, (
                        parsed["timestamp"], parsed["date"], parsed["model"],
                        parsed["input_tokens"], parsed["output_tokens"],
                        parsed["cache_read_tokens"], parsed["cache_creation_tokens"],
                        parsed["total_tokens"], line.strip()
                    ))
                    total_new += 1
            except json.JSONDecodeError:
                continue

        cursor.execute("""
            INSERT OR REPLACE INTO sync_state (file_path, last_line, synced_at)
            VALUES (?, ?, ?)
        """, (json_file, len(lines), datetime.now().isoformat()))

    conn.commit()
    conn.close()
    if total_new > 0:
        console.print(f"[dim]Synced {total_new} new records.[/dim]")


def format_tokens(n: int) -> str:
    """Format token count in human-readable form."""
    if n >= 1_000_000:
        return f"{n / 1_000_000:.1f}M"
    if n >= 1_000:
        return f"{n / 1_000:.1f}K"
    return str(n)


def compute_cost(input_t: int, output_t: int, cache_read_t: int, cache_create_t: int, model: str | None = None) -> float:
    """Compute cost for given token counts using model-specific pricing."""
    pricing = get_pricing(model)
    return (
        (input_t / 1_000_000) * pricing["input"]
        + (output_t / 1_000_000) * pricing["output"]
        + (cache_read_t / 1_000_000) * pricing["cache_read"]
        + (cache_create_t / 1_000_000) * pricing["cache_write"]
    )


def get_model_display_name(model: str | None) -> str:
    """Display name for a model (already normalized at parse time)."""
    return model or "unknown"


def get_stats(conn, days: int, label: str) -> dict:
    """Get usage statistics for the specified period."""
    cursor = conn.cursor()
    start_date = (datetime.now() - timedelta(days=days - 1)).strftime("%Y-%m-%d")

    # Overall totals
    cursor.execute("""
        SELECT
            COUNT(*) as request_count,
            COALESCE(SUM(input_tokens), 0),
            COALESCE(SUM(output_tokens), 0),
            COALESCE(SUM(cache_read_tokens), 0),
            COALESCE(SUM(cache_creation_tokens), 0),
            COALESCE(SUM(total_tokens), 0)
        FROM metrics WHERE date >= ?
    """, (start_date,))
    row = cursor.fetchone()

    # Per-model breakdown
    cursor.execute("""
        SELECT model,
               COUNT(*),
               COALESCE(SUM(input_tokens), 0),
               COALESCE(SUM(output_tokens), 0),
               COALESCE(SUM(cache_read_tokens), 0),
               COALESCE(SUM(cache_creation_tokens), 0)
        FROM metrics WHERE date >= ?
        GROUP BY model ORDER BY SUM(total_tokens) DESC
    """, (start_date,))
    by_model = cursor.fetchall()

    # Hourly distribution
    cursor.execute("""
        SELECT CAST(strftime('%H', timestamp) AS INTEGER) as hour, COUNT(*)
        FROM metrics WHERE date >= ?
        GROUP BY hour ORDER BY hour
    """, (start_date,))
    by_hour = cursor.fetchall()

    # Daily breakdown
    cursor.execute("""
        SELECT date, COUNT(*),
               COALESCE(SUM(input_tokens), 0),
               COALESCE(SUM(output_tokens), 0),
               COALESCE(SUM(cache_read_tokens), 0),
               COALESCE(SUM(cache_creation_tokens), 0)
        FROM metrics WHERE date >= ?
        GROUP BY date ORDER BY date DESC
    """, (start_date,))
    daily = cursor.fetchall()

    # Previous period for trend comparison
    prev_start = (datetime.now() - timedelta(days=2 * days - 1)).strftime("%Y-%m-%d")
    prev_end = (datetime.now() - timedelta(days=days)).strftime("%Y-%m-%d")
    cursor.execute("""
        SELECT COUNT(*),
               COALESCE(SUM(input_tokens), 0),
               COALESCE(SUM(output_tokens), 0),
               COALESCE(SUM(cache_read_tokens), 0),
               COALESCE(SUM(cache_creation_tokens), 0)
        FROM metrics WHERE date >= ? AND date <= ?
    """, (prev_start, prev_end))
    prev = cursor.fetchone()

    return {
        "period": label,
        "request_count": row[0],
        "input_tokens": row[1],
        "output_tokens": row[2],
        "cache_read_tokens": row[3],
        "cache_creation_tokens": row[4],
        "total_tokens": row[5],
        "by_model": by_model,
        "by_hour": by_hour,
        "daily_breakdown": daily,
        "prev_requests": prev[0],
        "prev_input": prev[1],
        "prev_output": prev[2],
        "prev_cache_read": prev[3],
        "prev_cache_create": prev[4],
    }


def trend_indicator(current: float, previous: float) -> str:
    """Return a colored trend string like '▲ +12%' or '▼ -5%'."""
    if previous == 0:
        return "[dim]--[/dim]"
    pct = ((current - previous) / previous) * 100
    if pct > 0:
        return f"[red]▲ +{pct:.0f}%[/red]"
    elif pct < 0:
        return f"[green]▼ {pct:.0f}%[/green]"
    return "[dim]─ 0%[/dim]"


def print_stats(stats: dict):
    """Print formatted statistics using Rich."""
    # Header
    console.print()
    console.print(Panel(
        f"[bold]CC-Insights[/bold] · {stats['period']}",
        style="cyan",
        expand=False,
    ))

    if stats["request_count"] == 0:
        console.print("\n  [dim]No data for this period.[/dim]\n")
        return

    # Compute total cost (model-aware)
    total_cost = 0.0
    for model, reqs, inp, out, cr, cc in stats["by_model"]:
        total_cost += compute_cost(inp, out, cr, cc, model)

    # Previous period cost
    prev_cost = compute_cost(
        stats["prev_input"], stats["prev_output"],
        stats["prev_cache_read"], stats["prev_cache_create"],
    )

    # Cache hit rate
    total_input = stats["input_tokens"] + stats["cache_read_tokens"]
    cache_hit = (stats["cache_read_tokens"] / total_input * 100) if total_input > 0 else 0

    # Overview line
    cost_trend = trend_indicator(total_cost, prev_cost)
    req_trend = trend_indicator(stats["request_count"], stats["prev_requests"])

    console.print()
    console.print(f"  Requests: [bold]{stats['request_count']:,}[/bold]  {req_trend}"
                  f"    Cost: [bold]${total_cost:.2f}[/bold]  {cost_trend}"
                  f"    Cache Hit: [bold]{cache_hit:.1f}%[/bold]")

    # Model breakdown table
    if stats["by_model"]:
        console.print()
        table = Table(box=box.ROUNDED, show_edge=True, pad_edge=True)
        table.add_column("Model", style="bold")
        table.add_column("Reqs", justify="right")
        table.add_column("Input", justify="right")
        table.add_column("Output", justify="right")
        table.add_column("Cache Read", justify="right")
        table.add_column("Cost", justify="right", style="yellow", no_wrap=True)
        table.add_column("Share", justify="left", no_wrap=True)

        for model, reqs, inp, out, cr, cc in stats["by_model"]:
            cost = compute_cost(inp, out, cr, cc, model)
            share_pct = (cost / total_cost * 100) if total_cost > 0 else 0
            bar_len = int(share_pct / 100 * 12)
            bar = "█" * bar_len + "░" * (12 - bar_len)

            table.add_row(
                get_model_display_name(model),
                str(reqs),
                format_tokens(inp),
                format_tokens(out),
                format_tokens(cr),
                f"${cost:.2f}",
                f"{bar} {share_pct:.0f}%",
            )

        console.print(table)

    # Peak hours
    if stats["by_hour"]:
        console.print()
        console.print("  [bold]Peak Hours[/bold]")
        max_count = max(count for _, count in stats["by_hour"])
        for hour, count in stats["by_hour"]:
            bar_len = int(count / max_count * 24) if max_count > 0 else 0
            bar = "█" * bar_len
            console.print(f"  [dim]{hour:02d}[/dim] {bar} {count}")

    # Daily breakdown
    if stats["daily_breakdown"] and len(stats["daily_breakdown"]) > 1:
        console.print()
        table = Table(box=box.SIMPLE, show_edge=False, pad_edge=True, title="Daily Breakdown")
        table.add_column("Date", style="dim")
        table.add_column("Reqs", justify="right")
        table.add_column("Tokens", justify="right")
        table.add_column("Cost", justify="right", style="yellow")

        for date, reqs, inp, out, cr, cc in stats["daily_breakdown"]:
            cost = compute_cost(inp, out, cr, cc)
            total = inp + out + cr + cc
            table.add_row(date, str(reqs), format_tokens(total), f"${cost:.2f}")

        console.print(table)

    console.print()


def check_forwarding_status():
    """Check if there are any failed forwards."""
    console.print()
    console.print(Panel("[bold]Upstream Forwarding Status[/bold]", style="cyan", expand=False))

    if not FAILED_DIR.exists():
        console.print("  [green]OK[/green] No failed directory found\n")
        return

    failed_files = list(FAILED_DIR.glob("*.jsonl"))
    if not failed_files:
        console.print("  [green]OK[/green] All data successfully forwarded\n")
    else:
        total_failed = 0
        for f in failed_files:
            with open(f, 'r') as fp:
                lines = len(fp.readlines())
                total_failed += lines
                console.print(f"  [red]![/red] {f.name}: {lines} failed records")
        console.print(f"\n  Total failed: [bold red]{total_failed}[/bold red] records\n")


def tail_logs():
    """Show recent metrics in real-time."""
    import time

    console.print("[dim]Watching for new metrics (Ctrl+C to stop)...[/dim]")

    today = datetime.now().strftime("%Y-%m-%d")
    log_file = RAW_DIR / f"metrics-{today}.jsonl"

    if not log_file.exists():
        console.print(f"[yellow]No log file yet for today:[/yellow] {log_file}")
        return

    with open(log_file, 'r') as f:
        f.seek(0, 2)  # Go to end
        while True:
            line = f.readline()
            if line:
                try:
                    json.loads(line)
                    ts = datetime.now().strftime("%H:%M:%S")
                    console.print(f"[dim]{ts}[/dim] New metric received")
                except Exception:
                    pass
            else:
                time.sleep(1)


def main():
    args = sys.argv[1:] if len(sys.argv) > 1 else []
    command = args[0] if args else "today"

    if command != "tail":
        sync_json_to_db()

    conn = init_db()

    try:
        if command in ("today", ""):
            print_stats(get_stats(conn, days=1, label="Today"))
        elif command == "week":
            print_stats(get_stats(conn, days=7, label="This Week"))
        elif command == "month":
            print_stats(get_stats(conn, days=30, label="This Month"))
        elif command == "sync":
            console.print("[green]Sync complete.[/green]")
        elif command == "check":
            check_forwarding_status()
        elif command == "tail":
            tail_logs()
        else:
            print(__doc__)
    finally:
        conn.close()


if __name__ == "__main__":
    main()
