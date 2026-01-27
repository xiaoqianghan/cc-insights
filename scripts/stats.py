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

# Data directory (configurable via environment)
DATA_DIR = Path(os.environ.get("CC_INSIGHTS_DATA_DIR", Path.home() / ".claude" / "cc-insights"))
RAW_DIR = DATA_DIR / "raw"
FAILED_DIR = DATA_DIR / "failed"
DB_PATH = DATA_DIR / "metrics.db"


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


def parse_otel_metric(data: dict, fallback_date: str = None) -> dict:
    """Parse OTEL metric data and extract relevant fields."""
    result = {
        "timestamp": None,
        "date": None,
        "model": None,
        "input_tokens": 0,
        "output_tokens": 0,
        "cache_read_tokens": 0,
        "cache_creation_tokens": 0,
        "total_tokens": 0,
    }

    try:
        if not isinstance(data, dict):
            return result

        latest_time_nano = 0
        models = set()

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
                            if attrs.get("model"):
                                models.add(attrs["model"])

                            if token_type == "input":
                                result["input_tokens"] += value
                            elif token_type == "output":
                                result["output_tokens"] += value
                            elif token_type == "cacheRead":
                                result["cache_read_tokens"] += value
                            elif token_type == "cacheCreation":
                                result["cache_creation_tokens"] += value

        if latest_time_nano > 0:
            ts = datetime.fromtimestamp(latest_time_nano / 1_000_000_000)
            result["timestamp"] = ts.isoformat()
            result["date"] = ts.strftime("%Y-%m-%d")
        elif fallback_date:
            result["timestamp"] = f"{fallback_date}T00:00:00"
            result["date"] = fallback_date
        else:
            result["timestamp"] = datetime.now().isoformat()
            result["date"] = datetime.now().strftime("%Y-%m-%d")

        result["model"] = ",".join(sorted(models)) if models else None
        result["total_tokens"] = (
            result["input_tokens"] + result["output_tokens"] +
            result["cache_read_tokens"] + result["cache_creation_tokens"]
        )

    except Exception as e:
        print(f"Warning: Error parsing metric: {e}")

    return result


def sync_json_to_db():
    """Sync JSON files to SQLite database."""
    if not RAW_DIR.exists():
        print(f"No data directory found: {RAW_DIR}")
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
                parsed = parse_otel_metric(data, fallback_date=fallback_date)
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
        print(f"Synced {total_new} new records to database.")


def get_stats(days: int = 1, label: str = "Today") -> dict:
    """Get usage statistics for the specified period."""
    conn = init_db()
    cursor = conn.cursor()

    start_date = (datetime.now() - timedelta(days=days-1)).strftime("%Y-%m-%d")

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

    cursor.execute("""
        SELECT date, COUNT(*), COALESCE(SUM(input_tokens), 0),
               COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cache_read_tokens), 0),
               COALESCE(SUM(cache_creation_tokens), 0)
        FROM metrics WHERE date >= ?
        GROUP BY date ORDER BY date DESC
    """, (start_date,))
    daily = cursor.fetchall()
    conn.close()

    return {
        "period": label,
        "request_count": row[0],
        "input_tokens": row[1],
        "output_tokens": row[2],
        "cache_read_tokens": row[3],
        "cache_creation_tokens": row[4],
        "total_tokens": row[5],
        "daily_breakdown": daily
    }


def print_stats(stats: dict):
    """Print formatted statistics."""
    print(f"\n{'='*60}")
    print(f"  Claude Code Usage - {stats['period']}")
    print(f"{'='*60}")
    print(f"  Total Requests:      {stats['request_count']:,}")
    print(f"  Input Tokens:        {stats['input_tokens']:,}")
    print(f"  Output Tokens:       {stats['output_tokens']:,}")
    print(f"  Cache Read Tokens:   {stats['cache_read_tokens']:,}")
    print(f"  Cache Create Tokens: {stats['cache_creation_tokens']:,}")
    print(f"  Total Tokens:        {stats['total_tokens']:,}")

    # Cost estimate (Opus 4.5 pricing)
    # Input: $15/M, Output: $75/M, Cache Read: $1.875/M, Cache Write: $18.75/M
    input_cost = (stats['input_tokens'] / 1_000_000) * 15
    output_cost = (stats['output_tokens'] / 1_000_000) * 75
    cache_read_cost = (stats['cache_read_tokens'] / 1_000_000) * 1.875
    cache_create_cost = (stats['cache_creation_tokens'] / 1_000_000) * 18.75
    total_cost = input_cost + output_cost + cache_read_cost + cache_create_cost
    print(f"  Est. Cost:           ${total_cost:.2f}")

    # Cache efficiency
    total_input = stats['input_tokens'] + stats['cache_read_tokens']
    if total_input > 0:
        cache_hit_rate = (stats['cache_read_tokens'] / total_input) * 100
        print(f"  Cache Hit Rate:      {cache_hit_rate:.1f}%")

    if stats['daily_breakdown']:
        print(f"\n  Daily Breakdown:")
        print(f"  {'-'*56}")
        for date, requests, input_t, output_t, cache_r, cache_c in stats['daily_breakdown']:
            total = input_t + output_t + cache_r + cache_c
            print(f"  {date}  |  {requests:>5} req  |  {total:>12,} tokens")

    print(f"{'='*60}\n")


def check_forwarding_status():
    """Check if there are any failed forwards."""
    print(f"\n{'='*50}")
    print("  Upstream Forwarding Status")
    print(f"{'='*50}")

    if not FAILED_DIR.exists():
        print("  [OK] No failed directory found")
        print(f"{'='*50}\n")
        return

    failed_files = list(FAILED_DIR.glob("*.jsonl"))
    if not failed_files:
        print("  [OK] All data successfully forwarded")
    else:
        total_failed = 0
        for f in failed_files:
            with open(f, 'r') as fp:
                lines = len(fp.readlines())
                total_failed += lines
                print(f"  [!] {f.name}: {lines} failed records")
        print(f"\n  Total failed: {total_failed} records")

    print(f"{'='*50}\n")


def tail_logs():
    """Show recent metrics in real-time."""
    import time

    print("Watching for new metrics (Ctrl+C to stop)...")

    today = datetime.now().strftime("%Y-%m-%d")
    log_file = RAW_DIR / f"metrics-{today}.jsonl"

    if not log_file.exists():
        print(f"No log file yet for today: {log_file}")
        return

    with open(log_file, 'r') as f:
        f.seek(0, 2)  # Go to end
        while True:
            line = f.readline()
            if line:
                try:
                    json.loads(line)
                    ts = datetime.now().strftime("%H:%M:%S")
                    print(f"[{ts}] New metric received")
                except:
                    pass
            else:
                time.sleep(1)


def main():
    args = sys.argv[1:] if len(sys.argv) > 1 else []
    command = args[0] if args else "today"

    if command != "tail":
        sync_json_to_db()

    if command in ("today", ""):
        print_stats(get_stats(days=1, label="Today"))
    elif command == "week":
        print_stats(get_stats(days=7, label="This Week"))
    elif command == "month":
        print_stats(get_stats(days=30, label="This Month"))
    elif command == "sync":
        print("Sync complete.")
    elif command == "check":
        check_forwarding_status()
    elif command == "tail":
        tail_logs()
    else:
        print(__doc__)


if __name__ == "__main__":
    main()
