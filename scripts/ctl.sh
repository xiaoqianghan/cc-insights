#!/bin/bash
#
# CC-Insights Control Script
#
# Usage:
#   cci start    - Start services (nginx + vector)
#   cci stop     - Stop services
#   cci status   - Check service status
#   cci restart  - Restart services
#   cci logs     - View Vector logs
#   cci test     - Send a test metric
#   cci stats    - Show usage statistics (today/week/month)
#

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DATA_DIR="${CC_INSIGHTS_DATA_DIR:-$HOME/.claude/cc-insights}"
LOG_FILE="/opt/homebrew/var/log/vector.log"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

start() {
    echo -e "${CYAN}Starting CC-Insights services...${NC}"
    brew services start nginx
    brew services start vector
    echo -e "${GREEN}Services started.${NC}"
}

stop() {
    echo -e "${CYAN}Stopping CC-Insights services...${NC}"
    brew services stop nginx
    brew services stop vector
    echo -e "${GREEN}Services stopped.${NC}"
}

restart() {
    echo -e "${CYAN}Restarting CC-Insights services...${NC}"
    brew services restart nginx
    brew services restart vector
    echo -e "${GREEN}Services restarted.${NC}"
}

status() {
    echo ""
    echo "═══════════════════════════════════════════════════════════"
    echo "  CC-Insights Status"
    echo "═══════════════════════════════════════════════════════════"

    # Check Nginx
    NGINX_STATUS=$(brew services list | grep nginx | awk '{print $2}')
    if [ "$NGINX_STATUS" = "started" ]; then
        echo -e "  Nginx:      ${GREEN}● Running${NC}"
    else
        echo -e "  Nginx:      ${RED}○ Stopped${NC}"
    fi

    # Check Vector
    VECTOR_STATUS=$(brew services list | grep vector | awk '{print $2}')
    if [ "$VECTOR_STATUS" = "started" ]; then
        echo -e "  Vector:     ${GREEN}● Running${NC}"
    else
        echo -e "  Vector:     ${RED}○ Stopped${NC}"
    fi

    # Check endpoint
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:4318/v1/metrics -X POST -d '{}' -H "Content-Type: application/json" 2>/dev/null)
    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "202" ]; then
        echo -e "  Endpoint:   ${GREEN}● Listening${NC} on :4318"
    else
        echo -e "  Endpoint:   ${RED}○ Not responding${NC} (HTTP $HTTP_CODE)"
    fi

    # Data stats
    RAW_COUNT=$(find "$DATA_DIR/raw" -name "*.jsonl" 2>/dev/null | wc -l | tr -d ' ')
    TODAY_FILE="$DATA_DIR/raw/metrics-$(date +%Y-%m-%d).jsonl"
    if [ -f "$TODAY_FILE" ]; then
        TODAY_RECORDS=$(wc -l < "$TODAY_FILE" | tr -d ' ')
    else
        TODAY_RECORDS=0
    fi

    echo "  ───────────────────────────────────────────────────────"
    echo "  Data files:   $RAW_COUNT"
    echo "  Today:        $TODAY_RECORDS records"
    echo "═══════════════════════════════════════════════════════════"
    echo ""
}

logs() {
    if [ -f "$LOG_FILE" ]; then
        tail -f "$LOG_FILE"
    else
        echo "No log file found: $LOG_FILE"
        echo "Try: brew services log vector"
    fi
}

test_endpoint() {
    echo -e "${CYAN}Sending test metric...${NC}"

    RESPONSE=$(curl -s -w "\n%{http_code}" -X POST http://127.0.0.1:4318/v1/metrics \
        -H "Content-Type: application/json" \
        -d '{
            "resourceMetrics": [{
                "resource": {
                    "attributes": [
                        {"key": "service.name", "value": {"stringValue": "cc-insights-test"}}
                    ]
                },
                "scopeMetrics": [{
                    "metrics": [{
                        "name": "test.metric",
                        "sum": {
                            "dataPoints": [{
                                "asInt": 1,
                                "timeUnixNano": "'$(date +%s)000000000'"
                            }]
                        }
                    }]
                }]
            }]
        }')

    HTTP_CODE=$(echo "$RESPONSE" | tail -1)

    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "202" ]; then
        echo -e "${GREEN}✓ Test metric sent successfully${NC}"
    else
        echo -e "${RED}✗ Failed (HTTP $HTTP_CODE)${NC}"
        echo "Make sure services are running: cci start"
    fi
}

stats() {
    CC_INSIGHTS_DATA_DIR="$DATA_DIR" python3 "$SCRIPT_DIR/stats.py" "$@"
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) restart ;;
    status)  status ;;
    logs)    logs ;;
    test)    test_endpoint ;;
    stats)
        shift
        stats "$@"
        ;;
    *)
        echo "CC-Insights - Claude Code Usage Analytics"
        echo ""
        echo "Usage: cci <command>"
        echo ""
        echo "Commands:"
        echo "  start     Start services (nginx + vector)"
        echo "  stop      Stop services"
        echo "  restart   Restart services"
        echo "  status    Check service status"
        echo "  logs      View Vector logs (tail -f)"
        echo "  test      Send test metric to verify setup"
        echo "  stats     Usage statistics (today/week/month)"
        echo ""
        echo "Examples:"
        echo "  cci stats          # Today's usage"
        echo "  cci stats week     # This week"
        echo "  cci stats month    # This month"
        exit 1
        ;;
esac
