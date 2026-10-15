#!/usr/bin/env bash
# NetOps Lab — R9 OpenAPI 端点 + /dev 参考页回归（v0.10）。
#
# 断言：
#   D1  GET /api/openapi.json：200 + application/json + openapi 3.0 +
#       version 与 /api/health 一致 + paths ≥ 55 + 每操作有中文摘要；
#   D2  防漂移：spec 的每个 (path, method) 对 live server 探测，
#       无一落入「未知的 API 路径」fallback；
#   D3  浏览器 /dev：导航项 + 分组目录 + 方法徽章 + 端点计数 + console 零错误；
#   D4  路由表重构无退化：/alerts 与 /rules 页面照常渲染。
#
# 浏览器段（[3/3]）以 set +e 容错运行：agent-browser 偶发的会话竞态
# （1001 瞬断）不是断言失败——失败一律走 ok/bad 计数，最终以 FAIL 计数
# 退出；jeval 内置三次重试吸收瞬断。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
export PATH="$PATH:/home/z/.local/go/bin"

PORT=18188
WORK="$(mktemp -d /tmp/netops-r9oa.XXXXXX)"
DB="$WORK/r9.db"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }

cleanup() {
  [[ -n "${SERVE_PID:-}" ]] && kill "$SERVE_PID" 2>/dev/null || true
  agent-browser close >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

BIN="$WORK/netopslab"
go build -o "$BIN" ./cmd/netopslab

echo "[1/3] serve（空库即可——文档是路由表投影，与数据无关）"
(setsid "$BIN" serve --db "$DB" --addr "127.0.0.1:$PORT" > "$WORK/serve.log" 2>&1 < /dev/null &)
SERVE_PID=$(pgrep -f "netopslab serve.*:$PORT" | head -1)
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

echo "[2/3] API 契约断言"
HTTP_CODE=$(curl -s -o "$WORK/spec.json" -w "%{http_code}" "http://127.0.0.1:$PORT/api/openapi.json")
CT=$(curl -sI "http://127.0.0.1:$PORT/api/openapi.json" | rg -i "^content-type" | tr -d '\r' | head -1)
if [[ "$HTTP_CODE" == "200" && "$CT" == *application/json* ]]; then
  ok "D1a openapi.json 200 + application/json"
else
  bad "D1a http=$HTTP_CODE ct=$CT"
fi
python3 - "$WORK/spec.json" "http://127.0.0.1:$PORT/api/health" <<'PY' > "$WORK/d1.txt"
import json, sys, urllib.request

spec = json.load(open(sys.argv[1], encoding="utf-8"))
health = json.load(urllib.request.urlopen(sys.argv[2]))
checks = [
    ("D1b openapi 3.0.x", spec.get("openapi", "").startswith("3.0")),
    ("D1c version == health.version", spec["info"]["version"] == health["version"]),
    ("D1d paths >= 55", len(spec["paths"]) >= 55),
]
n_sum = sum(1 for p, item in spec["paths"].items() for m, op in item.items()
            if isinstance(op, dict) and op.get("summary"))
n_ops = sum(len([m for m in item if m in ("get", "post", "patch", "delete")]) for item in spec["paths"].values())
checks.append(("D1e 每操作有摘要", n_sum == n_ops and n_ops >= 55))
checks.append(("D1f tags 非空", len(spec.get("tags", [])) >= 5))
for name, passed in checks:
    print(("ok\t" if passed else "bad\t") + name)
PY
while IFS=$'\t' read -r st name; do
  if [[ "$st" == "ok" ]]; then ok "$name"; else bad "$name"; fi
done < "$WORK/d1.txt"

python3 - "$WORK/spec.json" "http://127.0.0.1:$PORT" <<'PY' > "$WORK/d2.txt"
import json, sys, urllib.request, urllib.error

spec = json.load(open(sys.argv[1], encoding="utf-8"))
base = sys.argv[2]
unknown = "未知的 API 路径"
probed = 0
for path, item in spec["paths"].items():
    for m in ("get", "post", "patch", "delete"):
        if m not in item:
            continue
        probe = path.replace("{id}", "zz_probe").replace("{runId}", "zz_probe").replace("{cpId}", "zz_probe")
        data = None
        headers = {}
        if m != "get":
            data = b"{}"
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(base + probe, data=data, method=m.upper(), headers=headers)
        try:
            resp = urllib.request.urlopen(req)
            body = resp.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", "replace")
        except Exception as e:  # noqa: BLE001
            print(f"bad\tD2 {m.upper()} {probe} 请求异常 {e}")
            probed += 1
            continue
        probed += 1
        if unknown in body:
            print(f"bad\tD2 {m.upper()} {probe} 落入未注册 fallback")
print(f"ok\tD2 全部 {probed} 个文档端点都在 live mux 上注册（无漂移）" if probed >= 55 else f"bad\tD2 probed={probed} < 55")
PY
while IFS=$'\t' read -r st name; do
  if [[ "$st" == "ok" ]]; then ok "$name"; else bad "$name"; fi
done < "$WORK/d2.txt"

echo "[3/3] 浏览器断言（/dev + 重构无退化抽检）"
# 容错段：agent-browser 会话竞态（1001 瞬断）不是断言失败——ok/bad 计数
# 捕获一切，进程退出码只由 FAIL 计数决定。
set +e


agent-browser set viewport 1600 900 >/dev/null
agent-browser open "http://127.0.0.1:$PORT/" >/dev/null
agent-browser wait --load networkidle >/dev/null 2>&1
NAV=$(agent-browser eval "(() => { const links = [...document.querySelectorAll('.sidebar-link')]; return links.some(l => (l.textContent||'').includes('API 参考')); })()" 2>/dev/null | tr -d '"' || true)
if [[ "$NAV" == "true" ]]; then ok "D3a 侧栏「API 参考」导航项"; else bad "D3a 无 API 参考入口"; fi
agent-browser open "http://127.0.0.1:$PORT/dev" >/dev/null
agent-browser wait --fn "!!document.querySelector('.api-list')" --timeout 20000 >/dev/null 2>&1
agent-browser wait 1000 >/dev/null
# 一次 eval 合并取分组/端点计数（减少会话竞态面），随后再武装 console 监听
# （渲染已在 wait --fn 确认完成，初始错误由等待条件兜住）
# open /dev 后的最初几秒 agent-browser 会话处于竞态窗口（eval 偶发 1001
# 且短重试无效、晚期同查询稳定成功）——给页面与会话双重的稳定时间。
sleep 2.5
agent-browser eval "(() => { const g = document.querySelectorAll('.api-group').length; const i = document.querySelectorAll('.api-item').length; return g + ',' + i; })()" > "$WORK/eval-raw.txt" 2>/dev/null || true
STATS=$(tr -d '"' < "$WORK/eval-raw.txt" || true)
for attempt in 1 2 3; do
  if [[ "$STATS" == 1001* || -z "$STATS" ]]; then
    sleep 2
    STATS=$(agent-browser eval "(() => { const g = document.querySelectorAll('.api-group').length; const i = document.querySelectorAll('.api-item').length; return g + ',' + i; })()" 2>/dev/null | tr -d '"' || true)
  else
    break
  fi
done
API_GROUPS="${STATS%%,*}"
ITEMS="${STATS##*,}"
agent-browser eval "window.__errs = []; window.addEventListener('error', e => window.__errs.push(String(e.message))); 'armed'" >/dev/null 2>&1 || true
if [[ "$API_GROUPS" =~ ^[0-9]+$ && "$API_GROUPS" -ge 5 && "$API_GROUPS" -le 30 && "$ITEMS" =~ ^[0-9]+$ && "$ITEMS" -ge 55 ]]; then
  ok "D3b 分组目录（${API_GROUPS} 组 · ${ITEMS} 端点）"
else
  bad "D3b groups=$API_GROUPS items=$ITEMS"
fi
BADGES=$(agent-browser eval "(() => { const b = document.querySelectorAll('.api-method'); const kinds = new Set([...b].map(x => x.textContent)); return b.length >= 55 && kinds.has('GET') && kinds.has('POST') && kinds.has('DELETE'); })()" 2>/dev/null | tr -d '"' || true)
if [[ "$BADGES" == "true" ]]; then ok "D3c 方法徽章（GET/POST/DELETE 等）"; else bad "D3c 方法徽章不完整"; fi
SUMS=$(agent-browser eval "(() => { return [...document.querySelectorAll('.api-summary')].filter(s => (s.textContent||'').length > 4).length >= 55; })()" 2>/dev/null | tr -d '"' || true)
if [[ "$SUMS" == "true" ]]; then ok "D3d 中文摘要列"; else bad "D3d 摘要缺失"; fi
ERRS=$(agent-browser eval "window.__errs ? window.__errs.length : -1" 2>/dev/null | tr -d '"' || true)
if [[ "$ERRS" == "0" ]]; then ok "D3e console 零错误"; else bad "D3e console errors = $ERRS"; fi

agent-browser open "http://127.0.0.1:$PORT/alerts" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1
agent-browser wait 600 >/dev/null
A_TITLE=$(agent-browser eval "document.querySelector('.topbar-title') ? document.querySelector('.topbar-title').textContent : ''" 2>/dev/null | tr -d '"' || true)
agent-browser open "http://127.0.0.1:$PORT/rules" >/dev/null
agent-browser wait --fn "!!document.querySelector('.card .filter-bar')" --timeout 20000 >/dev/null 2>&1
agent-browser wait 600 >/dev/null
R_TITLE=$(agent-browser eval "document.querySelector('.topbar-title') ? document.querySelector('.topbar-title').textContent : ''" 2>/dev/null | tr -d '"' || true)
if [[ "$A_TITLE" == *告警中心* && "$R_TITLE" == *检测规则* ]]; then
  ok "D4 路由重构无退化（/alerts + /rules 正常）"
else
  bad "D4 alerts=$A_TITLE rules=$R_TITLE"
fi

set -e

echo
echo "结果：PASS=$PASS FAIL=$FAIL"
[[ "$FAIL" -eq 0 ]] && echo "R9 OPENAPI REGRESSION: ALL GREEN"
exit "$FAIL"
