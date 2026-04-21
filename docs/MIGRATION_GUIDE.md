# CC-Insights Migration Guide: Python/Nginx/Vector → Go

**Audience**: This document is intended for Claude Code to read and follow when guiding a user through migration. Do not just hand this document to the user — walk them through each step interactively, confirming success before proceeding.

## Prerequisites

Before starting, verify the user has:
- The cc-insights repository cloned
- Go 1.22+ installed (`go version`)
- An existing cc-insights setup (Nginx + Vector + Python) with data in `~/.claude/cc-insights/raw/`
- Their upstream OTEL endpoint info (usually Jellyfish URL + auth token)

## Migration Steps

### Step 1: Build and Install

```bash
cd <repo-root>
make install
```

This builds `cci` and installs it to `~/.local/bin/cci` (no sudo required).
Make sure `~/.local/bin` is in your `PATH` — `make install` will warn if it isn't.

**IMPORTANT**: The old system may have created a symlink at `/usr/local/bin/cci` pointing to `scripts/ctl.sh`. If one exists, remove it so it doesn't shadow the new user-level binary (depending on PATH order):

```bash
ls -la /usr/local/bin/cci
# If it shows -> scripts/ctl.sh (or any symlink), remove it:
sudo rm /usr/local/bin/cci
```

**Verify**:
- `which cci` — should show `~/.local/bin/cci`
- `file $(which cci)` — should show `Mach-O 64-bit executable` (not a symlink, not a shell script)
- `cci --help` — should show available commands (serve, stats, stop, status, config, migrate, uninstall-legacy)

### Step 2: Create Configuration

Read the user's existing Nginx config to extract upstream details:

```bash
cat /opt/homebrew/etc/nginx/servers/cc-insights.conf
```

Extract:
- **Upstream URL**: from the `proxy_pass` directive (e.g., `https://app.jellyfish.co/ingest-webhooks/claude/XXXX`)
- **Host header**: from `proxy_set_header Host` (e.g., `app.jellyfish.co`)

Also read the user's Claude Code OTEL settings for the auth token:

```bash
cat ~/.claude/settings.json
```

Extract:
- **Authorization token**: from `OTEL_EXPORTER_OTLP_HEADERS` (the `Authorization=Bearer ...` value)

Then create the config file at `~/.claude/cc-insights/config.toml`:

```toml
[proxy]
listen = "127.0.0.1:4318"
idle_timeout = "15m"

[upstream]
url = "<UPSTREAM_URL>"
timeout = "30s"

[upstream.headers]
Authorization = "Bearer <TOKEN>"
Host = "<UPSTREAM_HOST>"

[storage]
db_path = "~/.claude/cc-insights/metrics.db"
retention_days = 1825
```

**Verify**: Run `cci config` — should display the loaded configuration as JSON with the correct upstream URL.

### Step 3: Migrate Historical Data

The old system stored data as JSONL files. The new system uses SQLite directly. Import all historical data:

**IMPORTANT**: If `~/.claude/cc-insights/metrics.db` already exists from the old Python system, it has an incompatible schema. Back it up first:

```bash
# Only if metrics.db already exists
mv ~/.claude/cc-insights/metrics.db ~/.claude/cc-insights/metrics.db.old
```

Then run migration:

```bash
cci migrate
```

**Verify**:
- Output should show "Migrated <filename>: N records" for each JSONL file with N > 0 for files that contain token usage data
- Final line shows total records imported
- Run `cci stats month` to verify data is queryable and costs look reasonable

**If records are 0 for all files**: The most likely cause is the old metrics.db was not backed up. Check with `sqlite3 ~/.claude/cc-insights/metrics.db ".schema metrics"` — if it shows columns like `total_tokens`, `request_count`, `raw_data`, it's the old schema. Back it up and re-run migrate.

### Step 4: Switch Services

Stop old services and start the new proxy:

```bash
# Stop old services
brew services stop nginx
brew services stop vector

# Verify port is free
lsof -i :4318 -sTCP:LISTEN  # should return nothing

# Start new proxy
cci serve -d

# Verify
curl -s http://127.0.0.1:4318/health  # should return "ok"
cci status                              # should show PID and record count
```

**IMPORTANT**: The user's Claude Code `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` in `~/.claude/settings.json` does NOT need to change — it still points to `http://127.0.0.1:4318/v1/metrics`.

**Verify upstream forwarding**: After the user's next Claude Code interaction generates OTEL metrics, check:
```bash
cci status  # upstream failures should be 0
```

### Step 5: Configure Auto-Start

The proxy needs to start automatically when the user opens a terminal. The approach depends on their shell setup:

**For plain zsh (most users)**:
Add to `~/.zshrc`:
```bash
# Auto-start cci proxy if not running
if command -v cci &>/dev/null && ! lsof -i :4318 -sTCP:LISTEN &>/dev/null 2>&1; then
    cci serve -d 2>/dev/null
fi
```

**For nix-darwin / Home Manager users**:
Add to their `programs.zsh.initContent` (typically in `flake.nix` or a shell module):
```nix
programs.zsh.initContent = lib.mkAfter ''
  # Auto-start cci proxy if not running
  if command -v cci &>/dev/null && ! lsof -i :4318 -sTCP:LISTEN &>/dev/null 2>&1; then
    cci serve -d 2>/dev/null
  fi
'';
```
Then remind them to run their rebuild command (e.g., `darwin-rebuild switch`).

**For bash users**:
Same snippet in `~/.bashrc`.

### Step 6: Clean Up Legacy (Optional)

```bash
cci uninstall-legacy
```

This removes:
- `/opt/homebrew/etc/nginx/servers/cc-insights.conf`
- `/opt/homebrew/etc/vector/vector.yaml`

It preserves `~/.claude/cc-insights/raw/` (the old JSONL files). The user can delete these manually after confirming the migration is successful:

```bash
rm -rf ~/.claude/cc-insights/raw/
rm -f ~/.claude/cc-insights/metrics.db.old
```

## Post-Migration Verification Checklist

Walk the user through each of these checks:

- [ ] `cci status` shows proxy running with a PID
- [ ] `cci stats` shows today's data (may be empty if just migrated)
- [ ] `cci stats month` shows historical data with reasonable costs
- [ ] `curl http://127.0.0.1:4318/health` returns "ok"
- [ ] After a Claude Code interaction, `cci stats` count increases
- [ ] `cci status` shows 0 upstream failures (upstream forwarding works)

## Troubleshooting

### Proxy won't start: "address already in use"
Old nginx is still running. Run `brew services stop nginx` and `lsof -i :4318` to verify.

### Stats show $0.00 cost for a model
The model name may not match any pricing entry. Run `cci config` to check the pricing section. Unknown models default to $0 — add the model's pricing to `~/.claude/cc-insights/config.toml`.

### Migration imports 0 records
Old `metrics.db` has incompatible schema. Back it up (`mv metrics.db metrics.db.old`) and re-run `cci migrate`.

### Upstream failures increasing
Check `cci config` for correct upstream URL and auth token. Test manually:
```bash
curl -X POST <upstream_url> \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"test": true}'
```

### Proxy exits unexpectedly
Check idle timeout — default is 15 minutes of inactivity. If the user wants it longer, edit `idle_timeout` in config.toml. The proxy logs to stderr; run `cci serve` (without `-d`) to see output in foreground.

## Architecture Summary (for context)

The old system: `Claude Code → Nginx (:4318) → mirror to Vector (:4319) → JSONL files → Python sync → SQLite`

The new system: `Claude Code → cci serve (:4318) → direct SQLite write + async upstream forward`

Single Go binary, no runtime dependencies, auto-start on demand, idle timeout auto-exit.
