---
name: api-recon
description: Use this skill when collecting website API endpoints.
---

# API Recon (frontend endpoint reconnaissance)

Within the **authorized scope**, discover as completely as possible: **backend APIs** (paths, methods, parameters, response bodies), **frontend routes**, and **UI feature triggers** (tabs, dialogs, table actions, and so on).

---

## Boundaries and prohibitions (required for agents; violations exceed scope)

This skill covers **API and parameter reconnaissance only**. It does not cover vulnerability discovery or exploitation.

### Task boundaries

| Area | Allowed | Prohibited |
|---|---|---|
| **Goal** | Enumerate paths, methods, parameters, routes, and UI triggers | SQLi/XSS, authorization bypass, brute force, vulnerability fuzzing, request tampering attacks, and destructive actions |
| **Authentication** | Use hooks and stubs/mocks to bypass **client-side** login gates | Ask the user for or guess credentials; submit a real login form |
| **Runtime** | Hook interfaces without credentials and use mock responses to render the SPA's post-login shell | Flows that require a real backend session to continue |

### Dynamic analysis without credentials (Phase 3 default)

1. Use `preload.js` / `runtime_harvest.js` to **intercept and stub** login, permission, menu, and other bootstrap APIs.
2. Return mock bodies for business query APIs with the **correct structure and a success business code**; data may be empty.
3. Render post-login frontend pages even without a backend or with 401 responses, triggering more XHR/fetch/WebSocket requests.
4. **Empty data, blank tables, and placeholder UI are expected**. Do not switch to real login or vulnerability testing because of them.

In short, use mocks to expose frontend routes and mount components, and **record outbound requests only**. The useful observation is which APIs the frontend calls, regardless of backend response data.

### Strict process prohibitions

| Prohibited | Alternative |
|---|---|
| Grep/curl/Read the main `index-*.js` entry to extract API paths before Phase 1 is complete | Run `OUTDIR/harvest_static.py` |
| Write a replacement harvest script such as `extract_apis.py` | Modify and rerun `OUTDIR/harvest_static.py` |
| Repeat the same failed grep/command at least twice | Change approach: inspect tool logs, modify harvest, or consult the reference |
| Skip gates A/B and run the original files in `scripts/` | Copy them to OUTDIR and adapt them to the target |
| Use real username/password, OTP, OAuth, or similar authentication | Use stubs/mocks as described above |
| Skip stubs to obtain real data and test authorization bypass or injection | Record outbound requests only, within the recon boundary |
| Delete, export sensitive data, or perform bulk writes and other irreversible actions | Apply the same limit to coverage clicks |
| Claim all pages and APIs were found without runtime and dynamic enumeration | Meet the completion definition or state the limitation |
| Claim all parameters are known without a parameter trigger matrix and diff | Use the Phase 3b matrix and Phase 5 diff |
| Infer required/optional status from one runtime sample | Diff multiple samples or infer from validation rules/errors |

---

## Two-layer model and runtime modes

| Layer | Output | Limitations |
|---|---|---|
| **Static** (JS bundle) | Endpoint paths, draft routes, and candidate fields at payload construction sites | No HTTP methods; parameters require Phase 1b; misses URLs assembled at runtime |
| **Runtime** (live session) | Methods, bodies, responses, dynamic URLs, and WS/SSE; multi-sample diffs complete parameters | A page must render to make requests; one sample cannot establish required/optional status |

| Runtime mode | Engine | Use |
|---|---|---|
| **depth** | `runtime_harvest.js` (Puppeteer) | API inventory, methods/parameters/response bodies, WS/SSE, reproducible batch runs |
| **coverage** | browser + `preload.js` | Click tabs, dialogs, and tables for deeper feature coverage |
| **both** | depth, then coverage | Broadest coverage and longest runtime |

**Parameter method** (no universal script): use harvest/regex for paths; use **wider context around path anchors, UI binding chains, multi-sample diffs, and error-based inference** for parameters (grep recipes in section J of [reference.md](reference.md)).

---

## Completion definition

Claim recon is complete only when all of the following hold:

- [ ] **Static**: Phase 1 harvest produces `api_static.txt`, `routes.txt`, and `js/`.
- [ ] **Runtime**: At least one of depth or coverage runs; coverage/both requires a **working hook and dynamic enumeration loop**.
- [ ] **Post-login shell**: Visiting a business path does not return to `/login` (account for hash routes).
- [ ] **Parameters**: coverage/both completes the parameter trigger matrix and `param_samples.json`; Phase 5 merges `params_merged.json`.
- [ ] **Depth** (if module pages are blank): Reconstruct the Phase 4 permission tree and rerun until **module-level APIs** appear, beyond locale/bootstrap.
- [ ] **Delivery**: All Phase 5 outputs exist (see its output table); `insert_assets` writes service and endpoint assets.

---

## Scripts and gates

Files under `scripts/` are reference templates. **Do not run the originals directly and treat the output as final.**

**Rule**: Read first, adapt to the target, write to `OUTDIR` (for example, `recon/`), and record changes in `CHANGES.md`. If a template does not fit, rewrite it using the method and retain only its structure.

| Gate | When | Reference script → OUTDIR copy | Common required adaptations |
|---|---|---|---|
| **A (static)** | After Phase 0, **before the first** harvest/spider run | `harvest_static.py` / `spider_mpa.py` | **Default regex works on most sites**; change endpoint regex, webpack/Vite `publicPath`, or MPA exclusions/cookies only if the manifest or site's conventions require it |
| **B (runtime)** | After Phase 2, before depth/coverage | `runtime_harvest.js` / `preload.js` + `config.json` | Cookie/localStorage keys, neutralize success values, stubs, login regex, API prefix, hash/history |

**Required SPA order** (do not reorder; phase numbers take precedence over "explore before scripting"):

| Step | Required | Prohibited |
|---|---|---|
| After Phase 0 | Next Bash command = `python3 OUTDIR/harvest_static.py <URL> OUTDIR` | curl/grep/Read the main `index-*.js` entry (usually >500KB) |
| Gate A | Copy script → make small changes as needed → **run immediately** | Extract APIs by hand before deciding whether to harvest |
| Before Phase 1 is complete | Check output with `wc -l`; modify harvest and retry 404s | Write a separate extraction script; repeatedly grep undownloaded URLs |
| Starting in Phase 1b | Grep only `OUTDIR/js/*.js` | Use the main bundle in place of harvest |

- ✅ Copy `harvest_static.py` → optionally change regex → **run immediately**.
- ❌ Curl the main bundle → grep repeatedly → write a temporary extractor → run harvest last.
- **MPA**: The next Bash command after Phase 0 is `python3 OUTDIR/spider_mpa.py ...`.

---

## Tool and output constraints

| Constraint | Description |
|---|---|
| Large files | **Do not** Read/grep `index-*.js` files >100KB into context; process them in batches with OUTDIR scripts |
| Grep output | Use `\| head -20` or `-m 5`; keep only path summaries in the conversation, not bundle snippets |
| Checks | Use `wc -l` and `ls \| wc -l`; do not Read an entire directory |
| Initial regex probe | Optional, at most once, on a small chunk ≤50KB or HTML only; use harvest for the formal static pass |
| Reference | Recipes, templates, and troubleshooting are in [reference.md](reference.md); do not duplicate the full text inline |

---

## Execution roadmap

```
Phase 0 classification + OUTDIR
  → Gate A → Phase 1 harvest (run immediately)
  → Phase 1b parameter analysis
  → Phase 2 three authentication gates → config.json
  → Gate B → Phase 3 runtime + parameter matrix
  → Phase 4 permission tree (if needed) → rerun Phase 3
  → Phase 5 merge/report + use insert_assets to batch-insert every discovered service and endpoint API asset; do not omit any discovered asset
```

Check these in order. **Do not enter the next phase before completing the previous item.**

1. [ ] **Phase 0**: Identify SPA/MPA; create `OUTDIR` → [Phase 0](#phase-0--classification).
2. [ ] **Gate A + Phase 1**: Copy script → harvest **immediately** → verify with `wc -l` → [Phase 1](#phase-1--static).
3. [ ] **Phase 1b**: Expand path-anchor context + trace binding layer → `param_candidates.json` → [Phase 1b](#phase-1b--parameter-analysis).
4. [ ] **Phase 2**: Three authentication gates → `config.json` → [Phase 2](#phase-2--three-authentication-gates).
5. [ ] **Gate B**: Adapt runtime scripts → [Phase 3](#phase-3--runtime).
6. [ ] **Phase 3**: depth / coverage / both; confirm post-login shell; parameter trigger matrix → `param_samples.json`.
7. [ ] **Phase 4** (if needed): Permission tree → patch stubs → rerun Phase 3 → [Phase 4](#phase-4--permission-tree-reconstruction).
8. [ ] **Phase 5**: Merge outputs + report + `insert_assets` → [Phase 5](#phase-5--merge-and-report).

---

## Phase 0 — Classification

Fetch the entry HTML and **create `OUTDIR`** (do not edit the skill's `scripts/`):

- **SPA**: Empty shell + `<div id=app>` + chunks → Phases 1–5.
- **MPA**: SSR + `<form>`, without an endpoint bundle → after Gate A:

```bash
python3 recon/spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "session=..."] [--max 300] [--depth 5] [--exclude "logout|delete|destroy"]
```

Produces `forms.txt`, `links.txt`, and `api_inline.txt`. For an SPA with approximately zero forms, proceed to Phase 1.

---

## Phase 1 — Static

Follow [Scripts and gates](#scripts-and-gates) and [Tool and output constraints](#tool-and-output-constraints).

```bash
python3 recon/harvest_static.py <BASE_URL> <OUTDIR>
```

Harvest parses HTML scripts and the webpack/Vite manifest, downloads all lazy chunks, and produces `js/`, `api_static.txt`, `routes.txt`, and `chunkmap.txt`.

```bash
wc -l OUTDIR/api_static.txt OUTDIR/routes.txt
ls OUTDIR/js | wc -l
```

- Compare chunk count with the manifest; modify harvest and retry 404s rather than curling each chunk by hand.
- If `api_static.txt` has too few entries, broaden the endpoint regex in OUTDIR and rerun (see the reference).

### Phase 1b — Parameter analysis

Paths come from Phase 1; inspect parameter fields separately. Follow the grep rules in [Tool and output constraints](#tool-and-output-constraints).

**Completion criterion**: For important APIs, identify field names, transport location, inferred types, whether fields are required, sample values, and confidence.

#### 1b.0 — Transport forms

| Form | Parameter location | Static evidence to inspect first |
|---|---|---|
| REST JSON | body + query | `(params\|data\|body)\s*:\s*\{` near the path anchor |
| GraphQL | `variables` | gql template, `$page: Int` |
| Traditional form | urlencoded | `<form>`, `FormData` |
| File upload | multipart | `FormData.append` |
| Path parameter | `/user/:id` | Route table + `useParams` / `$route.params` |
| Encrypted/signed | Wrapped in `sign`/`data` | Hook encryption function inputs (reference section D) |

Output: Label each API with `transport: query|json|form|graphql|encrypted`.

#### 1b.1 — Expand context around path anchors

Use a known path as an anchor and widen the context window to find the payload construction object:

```bash
grep -n '"/api/user/list"' OUTDIR/js/*.js | head -20
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' OUTDIR/js/*.js | head -20
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' OUTDIR/js/*.js | head -20
```

| Wrapper layer | Parameter clues |
|---|---|
| axios instance | `data` / `params` |
| Shared request wrapper | Global fields inserted by an interceptor |
| OpenAPI client | Generated method signature |
| React Query / SWR | Hook's second argument |
| Vue composable | Composable inputs |

Type clues: `yup`/`zod`/rules, `Form.Item name=`, embedded Swagger.

→ `param_candidates.json`：`{ path, fields[], source: "static-callsite", confidence }`

#### 1b.2 — Binding layer

```
Form field → onFinish/handleSubmit → transform → API payload
```

| Binding source | Method |
|---|---|
| Form submit | Trace submit → transform → API |
| Table search | `getFieldsValue()` → `params` |
| Route | `:id` / `?tab=` |
| Interceptor | Global `tenantId`, pagination, sign |
| Enum select | `options` → API enum values |

In the DevTools call stack, trace from `fetch`/`XHR.send` up to the payload construction function.

#### 1b.3 — Three payload questions (distinct from Phase 2's authentication gates)

| Question | What to identify |
|---|---|
| **Assembly** | Where the payload is built and transformed |
| **Validation** | required, pattern, enum |
| **Transport** | path / query / body / multipart / headers |

While checking the interceptor gate (Phase 2), also inspect globally inserted fields (Authorization, `X-Tenant-Id`, sign).

#### 1b.4 — Connection to Phase 3

Candidate fields come from static and binding analysis. Establish **required, optional, and conditional dependencies** with the Phase 3 parameter matrix and diffs plus Phase 5 error-based inference.

---

## Phase 2 — Three authentication gates

Grep `OUTDIR/js/` (with `head`) and record findings in `config.json` (recipes in the reference):

| Gate | Question | Keywords |
|---|---|---|
| **Render gate** | How does the client determine login state? | `isLogin`, `getToken`, Cookie/localStorage |
| **Interceptor gate** | What triggers navigation to `/login`? | `response_code`, `errno`, axios interceptor |
| **Content gate** | Where do menus and permissions come from? | `menu`, `permission`, `role`, `acl`, `routes` |

Do not treat a localStorage key name as a credential; confirm it through chunks and the request chain.

**Exit = Gate B**: Record conclusions in `config.json` and adapt `OUTDIR/runtime_harvest.js` / `preload.js`.

### Phase 2b — API observation (optional)

Use `preload.js` in OUTDIR to confirm session key names, Authorization, and nested API URLs:

| Setting | Output |
|---|---|
| `recordDetail: true` | `__API_RECON_DETAIL__` |
| `observe.xhrHeaders: true` | Observed headers |
| `extractUrlsFromResponse: true` | Nested APIs in responses |
| `observe.storageReads/cookieReads: true` | Values to add to config |
| `neutralizeVueRouter: true` | `__API_RECON_ROUTES__` |

Export these after each coverage round: `__API_RECON_LOG__`, `__API_RECON_DETAIL__`, `__API_RECON_ROUTES__`, and `__API_RECON_OBSERVE__`.

---

## Phase 3 — Runtime

Gate B must be complete. Follow [Boundaries and prohibitions](#boundaries-and-prohibitions-required-for-agents-violations-exceed-scope) and the credential-free mock strategy.

Set `"runtimeMode": "depth" | "coverage" | "both"` in `config.json` (see the reference template).

### Hooks and stubs (shared by depth and coverage)

| Layer | Scope | Purpose |
|---|---|---|
| L1 exact | Auth/permission/bootstrap stubs | Pass first-screen authentication |
| L2 negative-code patch | All JSON responses | Change unauthenticated code to success |
| L3 fallback | `/api` and similar requests not matched by L1 | Return an empty success body to expose UI |

- **depth**: Fake auth + `forward` business-code patch + `stubs`; traverse `routes` (hash/history); produce `runtime_api.json`.
- **coverage**: Inject `preload.js` at **document-start** (CDP `addScriptToEvaluateOnNewDocument` or a Userscript).

Verify that `window.__API_RECON_PRELOAD__` exists and business paths do not return to `/login`.

```bash
cd recon && npm install
node runtime_harvest.js config.json
```

### 3b — Dynamic enumeration in coverage mode (required)

1. Main navigation/sidebar: Click each item and wait 1–3 seconds for network activity.
2. Tabs: `role=tab`, `.ant-tabs-tab`.
3. Tables: View/edit/details on the first row.
4. Toolbar: Export, filter, create (**avoid irreversible deletion**).
5. On entering each module: Merge APIs/routes.
6. SPA: Use controlled `pushState` for paths missing from `routes.txt` (prohibited for MPA).

**Parameter trigger matrix** (required): Record each operation type once per module and **diff multiple samples**:

| Action | Parameters usually added |
|---|---|
| Initial list view | Pagination + default filters |
| Search click | keyword, filter |
| Advanced filter | More optional fields |
| Create/edit | Full entity |
| Bulk/export/sort | `ids[]`, `exportType`, `sortField` |

**Outbound bodies and headers remain real under stubs**. Use requests as evidence. Record to `scan_raw.json`, `param_samples.json`, and `api_detail.json`.

- **Vue**: `neutralizeVueRouter: true` + document-start preload.
- **React**: `routes.txt` + sidebar clicks + `pushState`.
- **both**: Run 3a depth, then 3b coverage.

---

## Phase 4 — Permission tree reconstruction

**Trigger**: Module pages are blank or each route makes only bootstrap requests (such as locale), indicating the content gate has not passed.

| Observation | Meaning |
|---|---|
| Post-login shell loads | Render and interceptor gates passed |
| Missing sidebar items or blank page after a click | Stub shape or permission codes are incomplete |
| Each route makes the same few API calls | `v-if permission` did not pass |
| `routes.txt` has far fewer routes than the bundle | Complete it from the auth module |

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl)[^"]*"' OUTDIR/js/*.js | sort -u | head -30
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|menuList|authList' OUTDIR/js/*.js | head -20
```

Typical chain: `role_permissions` (flat codes) + `permissions/all` (tree) → `getResultTree` → `userRouteAuth[CODE].url`.

```bash
python3 recon/extract_route_map.py recon/js recon/
python3 recon/build_perm_tree.py recon/js recon/ --config recon/config.json
```

Intermediate outputs: `route_map.json`, `userRouteAuth.json`, `permissions_tree.json`, `*_stub.json`, and `perm_codes_all.txt`.

Check stubs: The outer `response_code` matches the interceptor gate; flat codes align with the tree; `routes` covers every link in `route_map`.

After updating `config.json`, **rerun Phase 3**. For large SPAs, adjust `waitUntil`, `routeTimeout`, and `perRouteMs` (see reference sections A3/I).

---

## Phase 5 — Merge and report

### Output table

| File | Phase | Contents |
|---|---|---|
| `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt` | 1 | Static bundles and paths |
| `param_candidates.json` | 1b | Candidate static parameter fields |
| `config.json` | 2 | Three gates + runtime configuration |
| `runtime_api.json` | 3a | Detailed depth recording (including WS/SSE) |
| `param_samples.json`, `scan_raw.json`, `api_detail.json` | 3b | Multiple samples, click log, details |
| `route_map.json` and others | 4 | Permission-tree intermediate files (if used) |
| `params_merged.json` | 5 | Merged parameter fields + confidence |
| `api_merged.txt` | 5 | `METHOD /path [params] [static\|runtime\|both]` |
| `site_map.json` | 5 | Routes, APIs, parameters, features, limitations |
| **insert_assets** | 5 | Write all service and endpoint assets to the asset store |

### 5b — Merge parameters

Diff `param_samples.json`. There is **no universal merge script**. See reference J7 for confidence rules (high/medium/low/not yet triggered).

### 5c — Infer from errors

Within authorized scope, an incomplete request may be sent to read a 400 response (**parameter recon, not vulnerability testing**), such as `field 'x' is required` or an enum error. Account for `data` wrappers, `variables`, and pre-encryption `bizData`.

The report must state runtimeMode, static/runtime API counts, parameter confidence, uncovered modules, and a summary of `CHANGES.md` relative to the reference scripts.

Suggested `site_map.json` structure:

```json
{
  "site": "https://example.com",
  "runtimeMode": "both",
  "appType": "vue-spa",
  "routeGuardStrategy": ["nav-neutralize", "L1-auth", "L2-patch", "forward"],
  "apisFromStatic": [],
  "apisFromRuntime": [],
  "apis": [],
  "params": [{ "method": "POST", "path": "/api/user/list", "transport": "json", "fields": [] }],
  "frontendRoutes": [],
  "routesVerifiedByClick": [],
  "featuresTriggered": [],
  "limitations": ""
}
```

See [reference.md](reference.md) for more fields and grep recipes.

---

## General notes

- **Framework independent**: The same lazy-load method applies to webpack/Vite/Angular.
- **Transport**: REST/JSON, GraphQL, WebSocket, SSE; gRPC-web is outside scope.
- **SSR**: Client-side fetches can be recorded; RSC/Server Actions cannot be enumerated completely.
- **Blind spots**: JSVMP, WASM, and strict HMAC/mTLS checks → use static analysis and state the limitation.
- **Parameter blind spots**: Conditional fields, hidden parameters, and WASM payload construction → label "not yet triggered" or "unreachable".
- **Static fallback**: When runtime is blocked, static analysis can still enumerate endpoints.

---

## Additional resources

- Grep recipes, `config.json` template, troubleshooting, hooks, parameter analysis section J, and site_map template: **[reference.md](reference.md)**.
- See the [Scripts and gates](#scripts-and-gates) table for reference script paths.
