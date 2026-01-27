#!/bin/bash
#
# CC-Insights Uninstaller
#

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

DATA_DIR="${CC_INSIGHTS_DATA_DIR:-$HOME/.claude/cc-insights}"

echo ""
echo -e "${CYAN}CC-Insights Uninstaller${NC}"
echo ""

# Confirm
echo -e "${YELLOW}This will remove:${NC}"
echo "  - Nginx config: /opt/homebrew/etc/nginx/servers/cc-insights.conf"
echo "  - Vector config: /opt/homebrew/etc/vector/vector.yaml"
echo "  - CLI command: /usr/local/bin/cci"
echo ""
echo -e "${YELLOW}Data directory will NOT be deleted:${NC} $DATA_DIR"
echo ""
read -p "Continue? [y/N] " -n 1 -r
echo ""

if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 1
fi

# Stop services
echo -e "${CYAN}Stopping services...${NC}"
brew services stop nginx 2>/dev/null || true
brew services stop vector 2>/dev/null || true

# Remove configs
echo -e "${CYAN}Removing configurations...${NC}"
rm -f /opt/homebrew/etc/nginx/servers/cc-insights.conf
rm -f /opt/homebrew/etc/vector/vector.yaml

# Remove CLI
echo -e "${CYAN}Removing CLI command...${NC}"
sudo rm -f /usr/local/bin/cci

# Restart services (they'll run with default/no config)
echo -e "${CYAN}Restarting services...${NC}"
brew services start nginx 2>/dev/null || true

echo ""
echo -e "${GREEN}Uninstall complete.${NC}"
echo ""
echo "Your data is preserved at: $DATA_DIR"
echo "To delete data: rm -rf $DATA_DIR"
echo ""
echo "To reinstall: ./install.sh"
echo ""
