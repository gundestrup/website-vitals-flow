#!/usr/bin/env bash
# <bitbar.title>wvf</bitbar.title>
# <bitbar.version>v1.0</bitbar.version>
# <bitbar.desc>website-vitals-flow server status + start/stop</bitbar.desc>
# <swiftbar.refreshOnOpen>true</swiftbar.refreshOnOpen>
#
# Install: symlink into ~/Library/Application Support/SwiftBar/Plugins
#   ln -s "$PWD/scripts/swiftbar/wvf.30s.sh" \
#     ~/Library/Application\ Support/SwiftBar/Plugins/wvf.30s.sh

WVF_BIN="${WVF_BIN:-$HOME/workspace/website-vitals-flow/bin/wvf}"
API="${WVF_API:-http://127.0.0.1:8420}"
JOBS=$(curl -fsm 3 "$API/api/jobs" 2>/dev/null)

if [ -z "$JOBS" ]; then
  echo "WVF ✕ | color=gray"
  echo "---"
  echo "server stopped"
  echo "Start server | bash='\"$WVF_BIN\"' param1=serve terminal=true refresh=true"
  exit 0
fi

RUN=$(echo "$JOBS" | /usr/bin/python3 -c "
import json,sys
j=json.load(sys.stdin)
run=[x for x in j if x.get('state')=='running']
que=[x for x in j if x.get('state')=='queued']
print(len(run), len(que))
for x in (run+que)[:6]:
    print(f\"{x['state'][:4]} {x.get('detail') or x.get('kind','job')} | size=11\")
" 2>/dev/null)

NR=$(echo "$RUN" | head -1 | awk '{print $1}'); NQ=$(echo "$RUN" | head -1 | awk '{print $2}')
if [ "$NR" -gt 0 ]; then M="WVF ▶$NR"
elif [ "$NQ" -gt 0 ]; then M="WVF ⏳$NQ"
else M="WVF ✓"; fi
echo "$M"
echo "---"
echo "server running — ${NR} running, ${NQ} queued | color=green"
echo "---"
echo "$RUN" | tail -n +2
[ "$NR$NQ" = "00" ] && echo "no active jobs | color=gray size=11"
echo "---"
echo "Open dashboard | href=$API"
echo "Scan all | bash=curl param1=-fs param2=-X param3=POST param4=\"$API/api/scan-all\" param5=-d param6='{}' terminal=false refresh=true"
echo "---"
echo "Stop server | bash=pkill param1='-f' param2='bin/wvf serve' terminal=false refresh=true"
