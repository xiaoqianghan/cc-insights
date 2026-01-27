#!/bin/bash
#
# CC-Insights Installer
# Collects Claude Code OTEL metrics locally while forwarding to upstream
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DATA_DIR="${CC_INSIGHTS_DATA_DIR:-$HOME/.claude/cc-insights}"

echo ""
echo -e "${CYAN}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║           CC-Insights Installer                           ║${NC}"
echo -e "${CYAN}║   Claude Code Usage Analytics & Local Metrics Storage     ║${NC}"
echo -e "${CYAN}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""

# Step 1: Check dependencies
echo -e "${CYAN}[1/6] Checking dependencies...${NC}"

if ! command -v brew &> /dev/null; then
    echo -e "${RED}Error: Homebrew is required. Install from https://brew.sh${NC}"
    exit 1
fi

# Install nginx and vector if needed
for pkg in nginx vector; do
    if brew list $pkg &>/dev/null; then
        echo -e "  ${GREEN}✓${NC} $pkg already installed"
    else
        echo -e "  Installing $pkg..."
        brew install $pkg
    fi
done

# Step 2: Get upstream configuration
echo ""
echo -e "${CYAN}[2/6] Configuring upstream...${NC}"
echo ""
echo "Enter your OTEL upstream URL (e.g., https://app.jellyfish.co/ingest-webhooks/claude/xxxx)"
echo "Leave empty to skip upstream forwarding (local-only mode):"
read -r UPSTREAM_URL

if [ -n "$UPSTREAM_URL" ]; then
    # Extract hostname from URL
    UPSTREAM_HOST=$(echo "$UPSTREAM_URL" | sed -E 's|https?://([^/]+).*|\1|')
    echo -e "  Upstream host: ${GREEN}$UPSTREAM_HOST${NC}"
else
    echo -e "  ${YELLOW}Running in local-only mode (no upstream forwarding)${NC}"
    UPSTREAM_URL="http://127.0.0.1:4319/v1/metrics"
    UPSTREAM_HOST="127.0.0.1"
fi

# Step 3: Create data directories
echo ""
echo -e "${CYAN}[3/6] Creating data directories...${NC}"

mkdir -p "$DATA_DIR"/{raw,failed,vector-data}
echo -e "  ${GREEN}✓${NC} Created $DATA_DIR"

# Step 4: Install configurations
echo ""
echo -e "${CYAN}[4/6] Installing configurations...${NC}"

# Nginx config
NGINX_CONF_DIR="/opt/homebrew/etc/nginx/servers"
mkdir -p "$NGINX_CONF_DIR"

sed -e "s|{{UPSTREAM_URL}}|$UPSTREAM_URL|g" \
    -e "s|{{UPSTREAM_HOST}}|$UPSTREAM_HOST|g" \
    "$SCRIPT_DIR/configs/nginx-otel-proxy.conf" > "$NGINX_CONF_DIR/cc-insights.conf"
echo -e "  ${GREEN}✓${NC} Nginx config: $NGINX_CONF_DIR/cc-insights.conf"

# Vector config
VECTOR_CONF="/opt/homebrew/etc/vector/vector.yaml"
mkdir -p "$(dirname "$VECTOR_CONF")"

sed -e "s|{{DATA_DIR}}|$DATA_DIR|g" \
    "$SCRIPT_DIR/configs/vector.yaml" > "$VECTOR_CONF"
echo -e "  ${GREEN}✓${NC} Vector config: $VECTOR_CONF"

# Step 5: Install CLI command
echo ""
echo -e "${CYAN}[5/6] Installing CLI command...${NC}"

chmod +x "$SCRIPT_DIR/scripts/ctl.sh"
chmod +x "$SCRIPT_DIR/scripts/stats.py"

# Create symlink for 'cci' command
CCI_LINK="/usr/local/bin/cci"
if [ -L "$CCI_LINK" ] || [ -f "$CCI_LINK" ]; then
    sudo rm -f "$CCI_LINK"
fi
sudo ln -sf "$SCRIPT_DIR/scripts/ctl.sh" "$CCI_LINK"
echo -e "  ${GREEN}✓${NC} CLI command installed: cci"

# Step 6: Start services
echo ""
echo -e "${CYAN}[6/6] Starting services...${NC}"

brew services restart nginx
brew services restart vector
sleep 2

# Verify
echo ""
echo -e "${CYAN}Verifying installation...${NC}"

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:4318/v1/metrics -X POST -d '{}' -H "Content-Type: application/json" 2>/dev/null || echo "000")

if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "202" ]; then
    echo -e "  ${GREEN}✓${NC} Endpoint responding on :4318"
else
    echo -e "  ${YELLOW}!${NC} Endpoint not responding (HTTP $HTTP_CODE)"
    echo "    Try: brew services restart nginx && brew services restart vector"
fi

# Done
echo ""
echo -e "${GREEN}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║           Installation Complete!                          ║${NC}"
echo -e "${GREEN}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""
echo "Next steps:"
echo ""
echo "  1. Configure Claude Code to use local endpoint:"
echo ""
echo "     Add to ~/.claude/settings.json:"
echo ""
echo '     {'
echo '       "env": {'
echo '         "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://127.0.0.1:4318/v1/metrics"'
echo '       }'
echo '     }'
echo ""
echo "  2. Test the setup:"
echo ""
echo "     cci test      # Send test metric"
echo "     cci status    # Check service status"
echo ""
echo "  3. View usage stats:"
echo ""
echo "     cci stats          # Today"
echo "     cci stats week     # This week"
echo "     cci stats month    # This month"
echo ""
