# api-recon — Reference manual

Grep recipes, a `config.json` template, and troubleshooting. Run every grep against the `js/` directory. For a single-line bundle, first use `js-beautify` or `sed 's/}/}\n/g'` if needed; raw grep with a context window usually suffices.

## Script notes

All files in `scripts/` are **reference templates**. Adapt them to the target site before running them. Typical changes:

| Script | Common adaptations |
|---|---|
| `harvest_static.py` | Endpoint regex, webpack/Vite manifest parsing, micro-frontend publicPath, retry/concurrency |
| `runtime_harvest.js` | Neutralize field names and success values, stub match rules and body shape, route source, WS recording, `waitUntil`/`routeTimeout`/`proxy` |
| `preload.js` | `loginPathRe`, L1 stubs, `neutralize.fields`, `apiPattern`, whether to enable L3, `recordDetail`, `observe.*`, `neutralizeVueRouter` |
| `spider_mpa.py` | `--exclude` destructive links, cookie, depth/max, same-origin filtering |
| `extract_route_map.py` | `routeMap` / `routeLink` regex, KEY naming pattern |
| `build_perm_tree.py` | `userRouteAuth` parsing, `ROOTS`/`PREFIX_PARENT` hierarchy heuristics, outer stub field names |
| `config.json` | Shared entry point for all site-specific parameters above |

Place adapted files in the task work directory (for example, `recon/`) and describe exact changes from the reference scripts in the report.

---

## A. Analyze the three gates

### A1. Render gate — "How is login state determined?"

```bash
grep -rhoaE '.{0,40}(isLogin|isAuthenticated|loggedIn|hasLogin|requireAuth)\b.{0,80}' js | head
grep -rhoaE 'function (getUser|getToken|getAuth)[0-9]?\([^)]*\)\{.{0,200}' js | head
grep -rhoaE '(localStorage|sessionStorage)\.getItem\("[^"]+"\)' js | sort -u
grep -rhoaE '(Cookies?|cookie)\.(get|load)\("[^"]+"\)' js | sort -u
grep -rhoaE '\batob\(|JSON\.parse\(|jwt|decode' js | head
```

Trace `isLogin = f(getUser())` → `getUser = decode(storage.read(KEY))` to determine the **storage key**, **storage type** (Cookie vs localStorage), and **encoding**:

| Encoding | How to supply a value in config |
|---|---|
| Plain string / `"1"` / token | `"value": "anything-truthy"` |
| `JSON.parse(x)` | `"value": "json:{\"id\":1,\"username\":\"admin\"}"` |
| `JSON.parse(atob(x))` | `"value": "b64json:{\"id\":1,\"username\":\"admin\"}"` |
| JWT | Unsigned/`alg:none` JWT or signature with a key found in the bundle |
| Encryption (SM2/AES/RSA) | Look for a hardcoded key; if the render gate only needs a decodable blob, it may be forged; otherwise use static analysis as fallback |

→ Write to `cookies` / `localStorage`.

### A2. Interceptor gate — "What triggers navigation to /login?"

```bash
grep -rhoaE '.{0,60}(interceptors\.response|axios|request\.use).{0,120}' js | head
grep -rhoaE '.{0,40}(response_code|errcode|errno|\bcode\b|\bret\b|\bstatus\b)\s*[=!]==?\s*[\-0-9]{1,4}.{0,60}' js | head -20
grep -rhoaE '.{0,40}(未登录|请重新登录|登录已过期|unauthorized|登录失效|授权|token.{0,10}invalid).{0,40}' js | head
grep -rhoaE '.{0,30}(location\.href|router\.(push|replace)|navigate)\([^)]*login[^)]*\)' js | head
```

Identify the **field name**, **success value** (usually `0` or `200`), and **failure values that trigger navigation**. Verify with a junk session:

```bash
curl -sk -X POST -H 'Cookie: <fakekey>=junk' https://target/api/<protected> -d '{}' -H 'Content-Type: application/json'
```

→ Write to `neutralize.fields` + `neutralize.success`.

### A3. Content gate — "Where do menus and permissions come from?"

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl|resource|nav)[^"]*"' js | sort -u
grep -rhoaE '.{0,30}(menus|permissions|menuList|routeList|authList|role_permissions)\b.{0,120}' js | head
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|hasPermission|checkAuth' js | head
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head
```

**Two data layers** (common in enterprise admin panels):

| API | Typical payload | Consumer |
|---|---|---|
| `.../role_permissions` | `{ permissions: string[], role_type }` | Route guard, button-level ACL |
| `.../permissions/all` | `tree[{ code, position, children }]` | Sidebar menu rendering |
| `userRouteAuth` in the bundle | `{ CODE: { url, name? } }` | Code → frontend path |
| `routeMap` in the bundle | `{ KEY: { name, link } }` | Alias resolution (webpack `o.DASHBOARD`) |

Read consumer code to confirm how `getResultTree(tree, permissions)` filters and which field `v-if` / `hasAuth(code)` checks.

**Manual forge** (small site): Build a permissive payload → `stubs`.

**Full permission tree reconstruction** (large site with a blank sidebar/submodules): See **section I**.

---

## B. config.json template

```json
{
  "baseUrl": "https://target/",
  "runtimeMode": "both",
  "chromium": "/usr/bin/chromium",

  "cookies": [
    { "name": "auth", "value": "b64json:{\"id\":1,\"username\":\"admin\",\"role\":\"admin\",\"func\":{},\"permissions\":[\"*\"]}" }
  ],
  "localStorage": { "token": "faketoken", "isLogin": "1" },

  "neutralize": {
    "fields": ["response_code", "code", "errno", "ret", "status"],
    "success": 0,
    "flags": { "success": true, "message": "ok" }
  },
  "forward": true,
  "loginUrlPattern": "/login",
  "apiPattern": "/api/|/rest/|/graphql",

  "mockTier": "L1+L2",
  "recordDetail": true,
  "observe": {
    "storageReads": false,
    "cookieReads": false,
    "xhrHeaders": true
  },
  "neutralizeVueRouter": true,
  "stubs": [
    {
      "match": "permissions/all|/menu|role_permissions",
      "body": {
        "response_code": 0, "code": 0,
        "data": {
          "permissions": ["*"],
          "menus": [
            { "name": "dashboard", "path": "/dashboard", "show": true, "children": [] },
            { "name": "alert", "path": "/alert", "show": true, "children": [] }
          ]
        }
      }
    }
  ],

  "explore": {
    "clickTabs": true,
    "clickTables": true,
    "pushStateFallback": true,
    "maxMenuItems": 50
  },

  "routes": ["/dashboard", "/alert", "/asset", "/device", "/report", "/config", "/system"],
  "waitMs": 1500, "perRouteMs": 900, "headless": true,
  "waitUntil": "domcontentloaded",
  "routeTimeout": 12000,
  "proxy": "",

  "captureResponses": true, "recordWs": true, "respMax": 600
}
```

Field descriptions:
- `runtimeMode`: `depth` (Puppeteer), `coverage` (browser MCP), `both`.
- `cookies[].value` prefixes: `b64json:` → base64(JSON); `json:` → raw JSON; no prefix → literal value.
- `forward: true` forwards real requests and rewrites code fields; `false` uses fully offline stubs.
- `mockTier`: Preload layers enabled in coverage mode, such as `L1+L2` or `L1+L2+L3`.
- `routes` comes from `routes.txt`; after forging a menu, the harness appends `<a href>` links automatically.
- `captureResponses` / `recordWs` apply only in depth mode.
- `waitUntil`: Use `domcontentloaded` for large SPAs to avoid `networkidle2` hanging.
- `routeTimeout`: Timeout in milliseconds for `page.goto` on one route.
- `proxy`: Puppeteer `--proxy-server`; `HTTP_PROXY` / `HTTPS_PROXY` are also available.

### B1. Two-stub template (role_permissions + permissions/all)

```json
"stubs": [
  {
    "match": "role_permissions",
    "body": {
      "response_code": 0,
      "data": {
        "permissions": ["MONITOR", "MONITOR_ALERT", "THREAT", "ASSETS_RISK"],
        "role_type": "SUPER_ADMIN"
      }
    }
  },
  {
    "match": "permissions/all",
    "body": {
      "response_code": 0,
      "data": [
        {
          "code": "MONITOR",
          "position": 1,
          "children": [
            { "code": "MONITOR_ALERT", "position": 1, "children": [] }
          ]
        }
      ]
    }
  }
]
```

Outer field names (`response_code` / `code` / `data`) must match the A2 interceptor gate; `permissions` must cover every leaf code in the tree.

---

## C. Coverage mode: preload configuration

Edit the `CONFIG` object at the top of `scripts/preload.js`, or replace it before CDP injection:

```javascript
const CONFIG = {
  loginPathRe: /\/(login|signin)(\/|$|\?)/i,
  mockTier: 'L1+L2',
  forward: true,
  recordDetail: true,
  extractUrlsFromResponse: true,
  neutralizeVueRouter: true,
  observe: { storageReads: false, cookieReads: false, xhrHeaders: true },
  neutralize: { fields: ['response_code', 'code'], success: 0 },
  stubs: [ /* same as config.json stubs */ ],
  apiPattern: /\/(api|apis|v\d+|dev|internal|graphql)\//i,
};
```

Verify `window.__API_RECON_PRELOAD__ === true` and a stable pathname.

Export recorded results:

```javascript
JSON.stringify({
  apis: [...window.__API_RECON_LOG__],
  detail: window.__API_RECON_DETAIL__,
  routes: [...(window.__API_RECON_ROUTES__ || [])],
  observe: window.__API_RECON_OBSERVE__,
}, null, 2)
```

---

## D. Preload / runtime hook capabilities

Built-in browser hooks and their coverage in preload (coverage) and runtime_harvest (depth):

| Hook capability | Value for API discovery | Coverage |
|---|---|---|
| Hook fetch / XHR.open | Record request URL/method | ✅ `recordDetail` + `__API_RECON_LOG__` |
| Hook XHR.setRequestHeader | Discover Authorization and other headers | ✅ `observe.xhrHeaders` |
| Hook localStorage/cookie reads | Confirm session key names | ⚠️ Optional `observe.storageReads/cookieReads` |
| Vue route capture | Complete frontendRoutes | ✅ `__API_RECON_ROUTES__` (loaded routes) |
| Vue route guard neutralization / login redirect blocking | Expose modules that trigger APIs | ✅ `neutralizeVueRouter` + native redirect neutralization |
| React route capture | Complete routes | ⚠️ Static analysis + clicks; no dedicated hook |
| Page navigation blocking (login path) | Keep the page available for analysis | ⚠️ Block only login paths, leaving business navigation intact |
| Hook crypto libraries (CryptoJS/SM, etc.) | Capture plaintext API bodies before parameter encryption | ❌ Hook encryption function inputs manually; record conclusions in config |
| Anti-debugging bypass | Otherwise runtime may record no APIs | ❌ Handle manually; static analysis remains available |

---

## E. Endpoint extraction regex (when static results are sparse)

Broaden `extract_endpoints` in `harvest_static.py`, or run manually:

```bash
grep -rhoaE '"/[a-z][A-Za-z0-9_/\-]{3,}"' js | sort -u
grep -rhoaE '/api/[a-zA-Z0-9_./-]+' js | sort -u
```

---

## F. Troubleshooting

| Symptom | Cause → action |
|---|---|
| Few static APIs | Endpoint convention does not match → broaden regex (section D) |
| Chunk count ≪ manifest | CSS-only or undeployed chunk; retry 404s |
| Runtime still shows login page | Render gate is wrong → recheck A1: key name, storage type, encoding, domain |
| Post-login shell loads but modules are blank | Content gate → forge menu (A3); `routes` paths may be wrong |
| Each route has only bootstrap/locale calls | Permission codes incomplete → reconstruct the permission tree in section I; check both `role_permissions` and `permissions/all` stubs |
| Sidebar has items but subpages are blank | Tree lacks intermediate nodes, or codes do not match `userRouteAuth` |
| Every API call redirects to login | Interceptor gate → check `neutralize`; nested fields require expanded walk logic |
| Zero WS frames | Subscription may require user interaction; increase `perRouteMs` |
| Empty response bodies | Real responses appear only with `forward: true` |
| Chromium missing | Install chromium or set `config.chromium` / `CHROMIUM` |
| Many mocks but still redirects to login | Hook runs too late or lacks `location.href` setter → document-start + preload |
| All lists are empty | L3 empty arrays are expected; continue through tabs/settings/details |
| Redux actions mistaken for routes | Filter internal paths containing get/set/change/clear/toggle/upload |
| Vue still redirects to login | Preload is not at document-start → change injection timing; or manually clear guards when `neutralizeVueRouter: false` |
| Response contains a URL absent from log | Enable `extractUrlsFromResponse`, or extract manually from `__API_RECON_DETAIL__` |
| Authorization header name unknown | Enable `observe.xhrHeaders` or inspect request headers in DevTools |
| Runtime is very slow / times out | Use `waitUntil: domcontentloaded`; lower `routeTimeout`; avoid `networkidle2` |
| Proxy connection fails | Check `proxy` / environment variables; use the same proxy port for Puppeteer and curl |

---

## G. Hardened targets

When the server validates the session at each step (signed cookies that cannot be forged, or server-rendered menus that cannot be stubbed), runtime may stop at the shell. Expected handling:

- **Static analysis is enough to enumerate endpoints** when module paths are in the code.
- If authorization permits, run the same harness with a **real session**: `forward: true`, without neutralize, to capture actual methods/parameters/responses.

---

## H. Per-task checklist

1. Confirm the authorized scope.
2. **Read** `scripts/harvest_static.py` → adapt it to the target → run it → review `api_static.txt` and `routes.txt`.
3. **Phase 1b**: Expand context around path anchors + trace the binding layer → `param_candidates.json` (section J).
4. Analyze A1/A2/A3 → write a site-specific `config.json`.
5. **Read and adapt** `runtime_harvest.js` / `preload.js` before running them.
6. `runtimeMode=depth`: `npm install` → run the adapted harvest script.
7. `runtimeMode=coverage/both`: Inject the adapted preload at document-start → use browser MCP for dynamic enumeration + the **parameter trigger matrix**.
8. If modules do not render, **reconstruct the permission tree in section I** → patch stubs → rerun.
9. Diff multiple parameter samples + infer from errors → `params_merged.json`.
10. Merge into `site_map.json` + `api_merged.txt`, and accurately state coverage, gaps, and script changes.

---

## I. Permission tree reconstruction (Phase 4 detail)

Use this when a simple forged `menus: [{ path, show: true }]` does not work and submodules still do not mount.

### I1. Locate the auth module

```bash
grep -l 'userRouteAuth' js/*.js
grep -l 'routeMap\|routeLink' js/*.js
grep -rhoaE 'getResultTree|role_permissions|permissions/all' js | head
```

Record the **permission API path**, **response field names**, and **consumer chunk filename**.

### I2. Extract routeMap

```bash
python3 scripts/extract_route_map.py recon/js recon/
# Produces recon/route_map.json
```

If `[!] no routeMap pattern found`, broaden the regex in `extract_route_map.py` or grep manually:

```bash
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head -20
```

### I3. Build the permission tree and stubs

```bash
python3 scripts/build_perm_tree.py recon/js recon/ --config recon/config.json
```

Script logic:
1. Parse `userRouteAuth={MONITOR:{url:...},...}` (including webpack aliases such as `He=o.DASHBOARD`).
2. Resolve aliases to actual paths with `route_map.json`.
3. Infer parents from code prefixes (`MONITOR_ALERT` → `MONITOR`).
4. Write `permissions_tree.json`, `permissions_all_stub.json`, and `role_permissions_stub.json`.
5. With `--config`, write `stubs` to `config.json` and extend `routes` automatically.

**Adapt to the target** (at the top of the script):
- `DEFAULT_ROOTS`: List of top-level module codes.
- `DEFAULT_PREFIX_PARENT`: `PREFIX_` → parent mapping.
- `DEFAULT_EXTRA_PARENT`: Orphan nodes without a prefix relationship.

### I4. Check stub consistency

```bash
# Permission count should approximately equal the userRouteAuth entry count
wc -l recon/perm_codes_all.txt
# Routes should cover every link in route_map
python3 -c "import json; m=json.load(open('recon/route_map.json')); r=set(json.load(open('recon/config.json'))['routes']); print('missing', [v['link'] for v in m.values() if v['link'] not in r])"
```

### I5. Rerun runtime and compare

```bash
node recon/runtime_harvest.js recon/config.json
# Compare runtime_api.json counts before and after forging; check for module APIs under /attack, /asset, etc.
```

| Before forging | After successful forging |
|---|---|
| Same 3–5 bootstrap calls per route | Different routes trigger different module APIs |
| Only `/api/locale/language` | `/api/web/...` module endpoints appear |
| Single-digit route count in `routes.txt` | 80–110+ `routes` from route_map |

### I6. If it still fails

- **Coverage mode**: Click sidebar items and tabs; permission gating may request data only after interaction.
- **Stub fields**: Compare nesting in the real API (curl + real session) with the stub.
- **Additional guards**: Grep button-level checks such as `hasPermission|checkRole|func.` and extend `role_permissions.permissions`.
- **Static fallback**: Module API paths remain in `api_static.txt`; runtime only adds methods/bodies. Preserve parameters from `param_candidates.json` and recorded samples.

---

## J. Parameter analysis (Phases 1b / 5b / 5c)

**A method, not a universal script.** Use regex to find paths. For parameters, widen context around path anchors, trace UI binding chains, diff multiple samples, and infer from errors.

### J1. Expand anchor context — find payload objects from paths

```bash
# Use a known Phase 1 path as an anchor
grep -n '"/api/user/list"' js/*.js
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' js | head
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' js | head
grep -rhoaE '(get|post|put|delete|patch)\([^,]+,\s*\{' js | head
```

### J2. Wrapper layers and transport forms

```bash
# axios / shared request wrapper
grep -rhoaE '(axios|request)\.(get|post|put|delete|patch)\(' js | head
grep -rhoaE 'interceptors\.(request|response)' js | head

# GraphQL
grep -rhoaE '(query|mutation)\s+\w+|gql`|graphql\(' js | head
grep -rhoaE '\$[a-zA-Z_]+\s*:\s*(Int|String|Boolean|\[)' js | head

# FormData / multipart
grep -rhoaE 'FormData|\.append\(' js | head

# Path parameters
grep -rhoaE 'path:\s*"/[^"]*:[^"]+"' js | head
grep -rhoaE 'useParams|route\.params|\$route\.params' js | head
```

### J3. Validation gate — required / format / enum

```bash
grep -rhoaE '(required|message|pattern|enum|validator)\s*:' js | head
grep -rhoaE 'yup\.|zod\.|async-validator|Form\.Item|a-form-item|el-form-item' js | head
grep -rhoaE 'rules\s*:\s*\[|name:\s*["\'][a-zA-Z_]+["\']' js | head
grep -rhoaE 'label.*value|options\s*:\s*\[' js | head
```

### J4. Binding layer — form → API

```bash
grep -rhoaE 'onFinish|handleSubmit|getFieldsValue|validateFields' js | head
grep -rhoaE '(pick|omit|transform|dayjs|moment)\(' js | head
```

Runtime follow-up: DevTools → Network → request → **Initiator** (call stack). Trace from `fetch`/`send` up to the payload construction function.

### J5. Encrypted parameters

```bash
grep -rhoaE 'encrypt|decrypt|sign|CryptoJS|sm2|sm3|sm4|RSA|AES' js | head
```

**Do not guess fields from ciphertext**. Hook the encryption function's **inputs** and record the plaintext payload before encryption. Record conclusions in `config.json` / `param_candidates.json`.

### J6. Parameter trigger matrix (required in Phase 3)

Record each operation once per module and diff request bodies/queries:

| Action | Inspect |
|---|---|
| Initial list view | Default pagination values |
| Search | keyword, filters |
| Advanced filter | Optional fields |
| Create/edit | Full entity |
| Bulk/export | `ids[]`, `exportType` |
| Sort/page change | `sortField`, `order` |

Produce `param_samples.json`: `[{ "path", "method", "action": "search", "body", "query", "headers" }]`.

### J7. Confidence rules

| Confidence | Condition |
|---|---|
| **High** | Static call site + at least two matching runtime samples |
| **Medium** | Static evidence only, or only one runtime sample |
| **Low** | Inferred from a response/error, without a second verification |
| **Not yet triggered** | Field known statically, but UI/permission path not reached |

### J8. Quick recipes by scenario

| Scenario | Sequence |
|---|---|
| REST list page | J1 payload object → J6 four diffs → J3 rules |
| Create/edit form | J3 Form name → J4 submit chain → runtime submission + deliberately blank fields to inspect 400 |
| GraphQL | J2 variables declaration → record variables for each runtime operation |
| Encrypted body | J5 Hook inputs → pre-encryption fields are the actual parameters |

### J9. Mapping to api-recon phases

| api-recon | Parameter recon |
|---|---|
| Phase 1 static | J1 expand anchor context |
| Phase 2 A2 interceptor | Globally inserted fields (tenantId, sign) |
| Phase 3 runtime | J6 trigger matrix + `param_samples.json` |
| Phase 4 permission tree | Forms differ by module; sufficient permissions are needed to trigger every field |
| Phase 5 merge | `params_merged.json` + confidence; do not infer required status from one sample |

### J10. Troubleshooting

| Symptom | Action |
|---|---|
| Field name known statically but never seen at runtime | Mark "not yet triggered"; complete the permission tree, click advanced filters, and try each linked select option |
| Different body shapes on the same path | Expected; record separately by `action` instead of forcing one schema |
| Stub response is fake but parameters are needed | **Inspect outbound request** bodies/headers; do not infer from the stub response |
| 400 names a nested field | Check outer wrappers `data`/`bizData`/`variables` |
| GraphQL shows only an operation name | Expand `variables` JSON; statically locate `$var: Type` |

---
