#!/usr/bin/env bash
# plugin lifecycle test — fake /api/jobs server + real install/uninstall.
set -u; cd "$(dirname "$0")" || exit 1; fail=0
T=$(mktemp -d); trap 'kill $SRV 2>/dev/null; rm -rf "$T"' EXIT

# 1) stopped state — nothing listening on :1
out=$(WVF_API=http://127.0.0.1:1 ./wvf.30s.sh)
if ! { echo "$out" | grep -q "WVF ✕" && echo "$out" | grep -q "server stopped"; }; then
  echo "FAIL: stopped state"; fail=1
fi

# 2) fake jobs api — /api/jobs maps to $T/api/jobs
mkdir -p "$T/api"
cat > "$T/api/jobs" <<'J'
[{"state":"running","detail":"scan x.dk","kind":"scan","id":1},
 {"state":"queued","detail":"scan y.dk","kind":"scan","id":2}]
J
python3 -m http.server 8491 --directory "$T" >/dev/null 2>&1 & SRV=$!
for _ in $(seq 1 20); do curl -fsm1 http://127.0.0.1:8491/api/jobs >/dev/null 2>&1 && break; sleep 0.3; done

out=$(WVF_API=http://127.0.0.1:8491 ./wvf.30s.sh)
if ! echo "$out" | grep -q "WVF ▶1"; then
  echo "FAIL: running state"; echo "$out" | head -3; fail=1
fi
if ! echo "$out" | grep -q "scan x.dk"; then
  echo "FAIL: job listed"; fail=1
fi

# 3) install/uninstall lifecycle into a temp plugins dir
SWIFTBAR_PLUGINS="$T/plugins" ./install.sh >/dev/null || { echo "FAIL: install"; fail=1; }
[[ -x "$T/plugins/wvf.30s.sh" ]] || { echo "FAIL: installed file"; fail=1; }
SWIFTBAR_PLUGINS="$T/plugins" ./uninstall.sh >/dev/null || { echo "FAIL: uninstall"; fail=1; }
[[ ! -e "$T/plugins/wvf.30s.sh" ]] || { echo "FAIL: file remains"; fail=1; }

if [[ $fail -eq 0 ]]; then echo "swiftbar plugin: all tests pass"; else exit 1; fi
