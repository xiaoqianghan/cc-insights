# CC-Insights

Claude Code OTEL metrics proxy and analytics tool. Single Go binary (`cci`) that receives OTEL metrics from Claude Code, forwards to upstream (Jellyfish), and stores locally in SQLite for cost analysis.

## Quick Reference

- **Build**: `make build`
- **Install**: `make install` (installs to `~/.local/bin/cci`, no sudo required)
- **Config**: `~/.claude/cc-insights/config.toml` (see `config.example.toml`)
- **Lint**: `make lint`
- **Test**: `make test`

## Migration from Nginx+Vector+Python

If the user is running the old stack (Nginx + Vector + Python `stats.py`), read and follow **`docs/MIGRATION_GUIDE.md`** to guide them through the migration step by step. That document is written for you (Claude Code) to execute interactively — walk the user through each step, verify before proceeding.

Signs the user is on the old stack:
- `brew services list` shows nginx/vector running
- `~/.claude/cc-insights/raw/` contains `.jsonl` files
- `scripts/ctl.sh` or `scripts/stats.py` exist (in older repo versions)
