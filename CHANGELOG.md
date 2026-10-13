# Changelog

Important changes to this project are recorded here, following [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/).

## [Unreleased]

## [0.3.15] - 2026-10-07

### Interception

#### Added

- **New built-in interception rule, "Deletion API paths"**: the existing destructive HTTP rule only recognised the DELETE **method**, while many applications allow deletion through GET/POST. Calls such as `curl 'http://t/api/user/delete?id=1'` really delete target data. A new `deny` rule covers `/delete /del /remove /unlink /erase /destroy` (a separator must follow the verb, so `/delivery`, `/details`, and `/delta` are not falsely blocked). Existing installations receive it on upgrade; it can be disabled or deleted under "System → Command interception".
- **Model fallback approval token usage is tracked separately**: approval calls previously had no separate usage attribution. A usage card below "Model fallback approval" in "System → Command interception" now shows cumulative calls, input, output, cache reads and writes, plus a small daily usage chart for the last 30 days.

### Finding notifications

#### Added

- **Send finding notifications to messaging services**: the new "System → Notifications" page supports six channel types: **DingTalk, Feishu, WeCom, generic Webhook, Telegram, and email**. Each type can have any number of instances, each with independent enablement, rate limits, and filters.
- **Real-time and digest delivery modes**: real-time mode sends each match immediately; digest mode combines matches at a global interval (30 minutes by default), with new finding counts and severity distribution at the top. Configure two channels to send high-severity findings immediately and the rest as digests.
- **Four filter dimensions**: minimum severity, task / asset scope, finding-type keyword inclusion and exclusion (exclusion takes priority), and whether to receive disposition status changes (off by default). Messages include a "View details" button using the global "Backlink URL"; leaving it blank omits the button.
- **Delivery history and manual resend**: view each delivery's status, attempts, failure reason, and channel; filter by channel and status. Failed deliveries can be resent with one click, resetting their retry counts.
- **Masked channel credentials**: Webhook URLs, signing secrets, bot tokens, SMTP passwords, and similar fields are returned as masks with a suffix hint. Submitting the unchanged mask means "keep current value"; clearing the input deletes the field.

#### Fixed

> This section records a security audit after the initial feature implementation. Each issue was reproduced, fixed, and covered by a regression test.

- **Fixed credential masking bypass through destination changes with retained credentials (critical)**: configuration merging preserved every omitted key, allowing a destination change to silently send stored credentials to an arbitrary address. Changing a destination now requires an explicit value for every credential field: a new value, or an empty value if no longer needed.
- **Fixed missing escaping of untrusted content in Markdown channels**: finding titles and summaries come from model output, and asset URLs from scan results. Crafted titles could render as external links in DingTalk / WeCom / Feishu, while image syntax could cause client fetches that expose readers' IP addresses. Content is now normalised to one line and Markdown metacharacters escaped at each channel's rendering boundary, rather than in the shared title function.
- **Fixed SSRF exposure in delivery destinations**: loopback, private-network, and cloud metadata destinations were previously allowed; the first 200 bytes of failure responses entered `last_error` and delivery history, creating a semi-blind read primitive. Protection now runs at the **dial stage**, covers DNS rebinding, and rejects redirects across hosts. Loopback and link-local addresses require `ARTEX_NOTIFY_ALLOW_LOCAL=1`; RFC1918 private networks remain allowed for common internal SMTP relays.
- **Fixed notification credentials leaking through errors**: DingTalk `access_token`, WeCom `key`, and Telegram bot tokens appear in URLs. Failed delivery previously exposed the full URL through `last_error`, delivery history, server logs, and frontend notices. Redaction now retains only `scheme://host` and the underlying cause.
- **Fixed silent loss when truncated digests marked the entire batch delivered**: omitted findings appeared in neither the message nor the failure list, despite successful delivery history. Digests now pack **whole entries**, mark only included entries delivered, and requeue the rest for the next message without consuming retries. The header states how many remain.
- **Fixed invalid minimum severity silently disabling filtering**: a typo such as `min_severity` set to `hgih` gave the unknown severity an ordinal of 0, making the condition always true and sending all findings despite an apparent high-severity threshold. Writes now validate values and list allowed options.
- **Fixed inaccessible `rate_per_min = 0` (unlimited)**: the database layer silently replaced an explicit 0 with the default. Defaults now apply at the API layer only when the field is omitted.
- **Fixed digest channels bypassing the token bucket**: `rate_per_min` previously had no effect in digest mode. Claim counts now respect both the current allowance and the memory limit.
- **Fixed batch failure handling using the highest attempt count**: an older delivery with two retries could cause new deliveries in the same batch to become failed permanently before receiving any retry. Outcomes are now decided per entry.
- **Fixed unparseable digest snapshots being silently marked successful**: entries skipped during rendering were previously counted as successful with the entire batch. They now fail explicitly with a reason.
- **Fixed per-channel delivery batches exceeding the lease duration**: in deployments with multiple instances, expired rows could be reclaimed, sent twice, and have attempts incremented twice. Each round's limit is now derived from lease duration / individual timeout, with assertions enforcing the constants' relationship.
- **Fixed Telegram truncation cutting HTML entities**: truncation avoided partial tags but not entity fragments such as `&amp`, which could cause rejection of the **entire** message. Both are now avoided.
- **Fixed email channels treating temporary SMTP failures as permanent**: greylisting responses such as `450` are temporary 4xx rejections and should be retried. Previously all were permanent failures after the first attempt. The first response-code digit now distinguishes retryable 4xx from permanent 5xx failures.
- **Fixed nested submissions storing literal credential masks**: object fields such as `webhook.headers` must be masked or submitted as a whole. Embedded mask sentinels were stored as real values and silently broke authentication. Such submissions are now rejected explicitly.

#### Design notes

- **Finding transactions perform one blind INSERT**: `notification_events` and findings are written in the same transaction for atomic consistency. The INSERT deliberately does not read channel tables or run user filters, and is wrapped in a `SAVEPOINT`; otherwise a misconfigured filter could abort the transaction and prevent a high-severity finding from being saved.
- **Delivery uses leases instead of long transactions**: after claiming with `FOR UPDATE SKIP LOCKED`, rows become `sending` and `next_attempt_at` is moved into the future as a lease. No database lock is held during delivery. Rows left by a crashed process are reclaimed after expiry, with bounded retries.
- **Rate limiting does not consume retries**: calculate the token-bucket allowance first, then claim that many deliveries. Reversing this order would count waiting as attempts and exhaust the three-attempt budget without delivery.

### Test infrastructure

#### Fixed

- **Fixed cleanup order permanently leaving assets in company ICP attribution tests**: `defer d.Close()` ran before `t.Cleanup`, so cleanup used a closed connection and discarded its errors, leaving test assets and companies in the database and breaking unrelated asset-count assertions. Connection closure now also uses `t.Cleanup`, registered first, and cleanup failures are reported.

### Traffic

#### Added

- **Traffic content search, path / status / length filters, and column sorting**: filters support **response content** through the full-text index, **path**, **status code** (exact or buckets such as `2xx`), and **response length ranges**. Headers sort by time, status, or response length, with preferences stored locally. Two new indexes apply automatically on upgrade; the Agent tool query contract is unchanged.
- **New "Clear all" traffic action**: deletes all traffic regardless of current filters, removes historical host directories absent from the index, performs full compaction, and reports released space. Traffic evidence linked to findings is stored separately and is unaffected.

#### Fixed

- **Fixed disk space not being reclaimed after deleting traffic**: index creation did not enable `auto_vacuum`, so files never shrank. The `contentless_delete` full-text index only wrote tombstones on `DELETE` without reclaiming postings, making deletion enlarge the index (a measured 6MB body produced a 16MB index that remained 16MB after deletion). New indexes use `auto_vacuum=incremental`; deletion triggers background full-text merging and incremental reclamation in chunks (104KB in the same scenario). Write locks are released between chunks, allowing recording to continue.

> Upgrade note: `auto_vacuum` must be set when creating the database, so **existing indexes remain in the old mode**, where incremental reclamation does nothing (startup logs a notice). Upgrades stop tombstone growth; use "Clear all" once to reclaim existing space and convert the index to incremental reclamation. Routine deletions then reclaim space automatically.

### LLM

#### Fixed

- **Fixed custom session headers missing from one-shot LLM calls**: goal decomposition (round 0) and cold-node compaction are one-shot calls without a transcript store or session ID in context, so gateways could not read the header. Endpoints such as opencode zen returned 400 in round 0 while later Planner rounds worked. Both paths now attach a session ID stable per exploration, also allowing token usage to be attributed to that exploration.

### Tools

#### Fixed

- **Fixed MCP and custom tool output truncation failing and overflowing context**: MCP responses were never truncated; HTTP custom tools received an empty tool context and used a hard-coded 6000-character limit instead of session configuration. Both now use `actool.Capture`, truncating at session `MaxOutputChars` (30000 by default). When `ToolOutputDir` is set, full output is written to disk and messages retain an opening excerpt and file pointer.
- **Upgraded norma to v0.4.3**: the harness applies `CaptureOnce` consistently so all tools, including future additions, receive output truncation.

### Deployment

#### Fixed

- **Fixed activity streams failing behind reverse proxies**: the SSE URL always used `${hostname}:8787`, pointing pages loaded from `https://域名/` (domain placeholder) behind a port-443 proxy to a port not exposed publicly and causing repeated reconnections. It now follows `NODE_ENV`: production defaults to the same origin; only `next dev` retains `:8787` to bypass Next.js SSE buffering. The `NEXT_PUBLIC_SSE_BASE` override remains. README now documents reverse proxy deployment.

### Findings

#### Fixed

- **Fixed missing scrolling in overflowing "By asset" lists**: the outer card had `max-height` without a definite height, so the scrolling viewport's percentage height could not resolve. Large lists overflowed or were clipped. The asset tree's native scroll container now receives a height limit that adapts to the window.

### Database

#### Fixed

- **Limited the connection pool**: `database/sql` otherwise opens connections without an application limit until PostgreSQL rejects them at `max_connections` (100 by default). The pool is capped at 32, with excess queries waiting for a free connection instead of failing. Lower this cap if PostgreSQL's limit has been reduced.
- **Improved connection reuse**: default `MaxIdleConns` of 2 closed excess connections after use, forcing new TCP and PostgreSQL authentication handshakes. The idle cap now matches the total cap, with a 30-minute lifetime and 5-minute idle expiry to replace stale connections naturally.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.14] - 2026-09-24

### Task list

#### Added

- **New "Findings" task column**: shows Critical / High / Medium / Low counts, colouring nonzero counts by severity to show each task's finding volume and distribution.

#### Changed

- **Narrower Description / Goals columns**: long content is truncated with full text on hover, reducing horizontal space consumed by long entries.

### Exploration graph and planning overview

#### Changed

- **Smaller planning overview (`graph_overview`) with list limits**: recent facts, ended intents, pending intents, cold digests, and confirmed finding details now show the latest window plus total counts, with omitted entries available on demand. This reduces per-round LLM context growth in long tasks; linked-task cold overviews are also limited.

#### Fixed

- **Removed reverse duplicate edges between digest and finding nodes after folding cold nodes in the exploration graph**.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.13] - 2026-09-19

### Asset interception

#### Added

- **New global asset interception rules (blocklist)**: exact and fuzzy matching for domains / IPs / URLs and CIDR networks, with create, read, update, delete, and enable/disable controls under "System → Asset interception". Built-in fuzzy rules block government (`.gov` / `.gov.cn`) and education (`.edu` / `.edu.cn`) sites by default.
- **Asset interception in the execution path**: before issuing an intent (`add_intent`) or inserting assets (`insert_assets`), the Agent checks target assets. Blocked intents are not issued and blocked assets are not inserted; asset details and the blocking reason are returned to the Agent.
- **New task-level block / allow rules (allowlist)**: independent of global rules and applying only to the current task. Block rules run first; a block match forbids testing. If allow rules exist and none match, testing is disallowed. Without allow rules, no allowlist is enforced. Rules can be entered at task creation or managed in the task Overview.
- **Task templates store categories and task-level block / allow rules**: applying a template copies its category and rules into the new-task form.

### Operation review

#### Fixed

- **Tighter model judge output protocol reduces accidental approval after truncation**: the `comment` limit drops from 500 to 120 Chinese characters and output must be JSON only, without preamble or code fences. This reduces unparsable `MaxTokens` truncation and consequent fail-open approval under the model failure policy.

### Task archives

#### Fixed

- **Archives skip symbolic links instead of failing the whole package**: the archive format supports only regular files and directories. Links are now skipped and logged, while other files are archived without following links or leaving the directory tree.

### Accounts and compliance

#### Added

- **New pre-login "Usage notice and disclaimer" dialog**: users must tick agreement before logging in.

### Licensing and dependencies

#### Changed

- **Adopted the AGPL-3.0 open-source licence** and expanded README licensing and disclaimer documentation.
- **Upgraded norma to v0.4.1**.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.12] - 2026-09-17

### Exploration timeline

#### Added

- **New exploration timeline on task details** (#144): presents exploration nodes (start / goal / intent / fact / finding / hint / digest) chronologically with type filters, keyword search, ordering, pagination, and auto-refresh. The first page with newest entries first is the live view and refreshes every round. Elsewhere only the unread count increases; "N new updates · Back to latest" returns to the live view. Entries are grouped by day for navigation through long tasks.
- **Timeline rows display node IDs** (#145), making it easier to cross-reference the exploration graph and locate nodes.
- **Expanded timeline details show upstream / downstream relationships and anchored assets** (#147): hovering a related entry shows a node card (type / status / source / time / summary / payload excerpt). Anchored assets appear with type labels and readable text. Data is included with the timeline page; expansion makes no extra request.
- **Timeline search supports node IDs** (#150): search now matches node IDs as well as content / source. A plain number or displayed form such as "#41" locates that node exactly.

### Intent management

#### Added

- **Soft and hard intent deletion modes** (#149): pending, running, and paused intents can be deleted with a required reason; choose the mode in the confirmation dialog.
  - **Soft deletion (default)**: marks the intent "Deleted" and stores the reason separately, preserving the node, all outputs, and lineage.
  - **Hard deletion**: physically removes the intent and descendants supported exclusively by it, cascading along output / intent chains to leaves to avoid orphan data. Deleted intent token usage is archived by original date. Shared nodes, goals, and task root facts are retained. The dialog shows the estimated cascade node count.

  Both modes notify the Planner that the user deleted the intent, include the reason, and trigger replanning.

### Operation review

#### Added

- **Approval records can be filtered by status and decision source** (#139) to locate records quickly.

#### Fixed

- **Pending approval requests load independently of history pagination** (#133) and remain fully visible while browsing historical records.

### Agent

#### Changed

- **Worker role description now uses general cybersecurity platform wording** (#138).

#### Fixed

- **Fixed cold-node compaction never triggering**: task Planners now receive the Compactor, restoring previously disconnected cold-digest execution.

### Chat

#### Added

- **Chat supports @ references to multiple record types and scroll pagination** (#135): reference candidates load additional pages on scroll.

#### Fixed

- **Long message bubbles stay within the conversation panel** (#137).
- **Fixed "Missing upload file" before a new conversation exists**: Composer now snapshots `FileList` into an array before clearing the input and calling back. Previously draft conversations required asynchronous creation first; when execution resumed, the live-bound `FileList` was empty, causing a missing upload field and backend 400.

### Traffic

#### Fixed

- **Traffic recording proxy listens only on 127.0.0.1 by default** (#129, #130), preventing an open proxy on other network interfaces.

### Web

#### Fixed

- **Fixed the global header missing from the statically exported task list**.
- **Added related traffic mocks to demo finding details, fixing a blank page**.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)
- [@dingpotian](https://github.com/dingpotian)

## [0.3.11] - 2026-09-15

### Operation review

#### Added

- **Approval records link to their source** (#125): clicking "Source" opens the exact tool execution that triggered approval in its full conversation, loads pages to its position, expands command and output, and centres and highlights it while keeping surrounding messages readable. Manual scrolling stops automatic adjustment. Persistent `tool_use_id` and task mapping locate calls across regular chats and segmented Worker, Planner, and Main agent sessions, with a new index on scoped call IDs. Missing, duplicate, or ambiguous links produce an explicit notice. Deleted conversations, archived tasks, and missing records also produce clear notices.
- **Approval record pagination** (#115).

#### Changed

- **Simplified model review input** (#124, #125): fallback LLM review now receives versioned JSON containing the current complete tool call, explicitly selected brief background, and local working directory. Background contains only real current user messages from chat or task Main agent sessions. Workers no longer include intent summaries or inherited background; Planner and automatically triggered sessions do not invent user messages. Task descriptions, goals, constraints, global exploration state, historical calls, and full Worker intents remain available for execution and independent session auditing but are excluded from operation review. Decisions must output JSON `decision`/`comment`, explaining the actual operation, consequences of success, and matching rule. Instructions in parameters or background cannot change review policy. Actual model inputs and fingerprints are saved; older snapshots retain their version and are never reconstructed from current data.

#### Fixed

- **Code-fenced model decisions no longer cause silent approval** (#126): fenced decision JSON previously failed parsing and could be allowed under the model failure policy. Code fences are now stripped before parsing; only a remaining parse failure invokes the configured failure policy.

### Agent

#### Added

- **New experimental noa context compaction (norma v0.4.0)**: disabled by default and enabled in system settings. noa model-driven compaction replaces built-in compaction for Main agent, Planner, Worker, and chat. Original content is archived centrally under `<workDir>/noa/<会话ID>/` (session ID placeholder), with globally unique session directories. Integration failure falls back to built-in compaction without interrupting tasks. The setting is read once per run and affects subsequently started runs.

#### Fixed

- **Exploration graph tools reject calls without task context instead of crashing on nil**: such calls now return a clear error instead of dereferencing an empty store.

### MCP

#### Added

- **Support for legacy SSE MCP services** (#117).

### Traffic

#### Fixed

- **Port-aware host matching in traffic search** (#114): `traffic_search` includes ports when matching hosts, preventing records on different ports from being mixed.
- **A failed `traffic_search` description migration no longer stops subsequent reporter migrations**: individual migration failures are isolated.

### Web

#### Changed

- **Separators between finding retest options** (#122) improve readability.

#### Fixed

- **Fixed demo task details crashing**: in mock mode, the Conversations tab requests `GET /api/tasks/<id>/side-questions`, but the mock handler lacked this route. Its fallback treated paths ending in s as collections and returned `[]`, leaving `data.items` as `undefined`. Iteration in the side-question hook's `merge()` threw `TypeError: t is not iterable` inside a `setItems` updater; React rethrew it during render, beyond the caller's `catch`, so the error boundary displayed "This page couldn't load". The mock now explicitly returns empty side-question history, and `sideAPI.history` normalises non-array `items` to `[]` as an additional safeguard.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.10] - 2026-09-13

### Network

#### Added

- **New official DeepSeek web search source**: reuses the active LLM profile. DeepSeek has no directly callable search API; search runs inside its Anthropic-compatible API through the server tool `web_search_20250305`. It therefore **requires an official DeepSeek endpoint and the anthropic protocol**; OpenAI endpoints reject server tools. Each search **uses an additional model call**, **bypasses the search egress proxy**, **is absent from recorded traffic**, and returns **titles and links only**, with content fetched by WebFetch when needed. Settings explain these restrictions without enforcing validation; users can verify through "Test search".

### Agent

#### Added

- **Workers can review traces across work runs**: `search_all_worker_traces` searches matching steps across all work in the task without a known intent_id; `get_worker_trace` lists a selected run's steps, searches by keyword, and retrieves full content by step_id. These support reusing observations that were not recorded as facts.
- **`node_detail` is available to Workers**: after obtaining an intent_id / node ID from trace tools, Workers can retrieve complete node details.
- **Planning rounds triggered by `add_hint` are explicitly announced**: previously hints were only included in the overview. Each `add_hint` now records one trigger stating that a user added N strategic hints, treating a batch as one event, and shows the Planner both the trigger and hint content.

#### Changed

- **Global overview guidance uses less restrictive wording**: encourages broader exploration and timely reporting of clues across intents instead of premature convergence.
- **Simplified Worker boundary wording**: An initial obstacle does not mean the path has been fully explored. Focus on trying the bypass methods within the current intent before drawing a conclusion.
- Removed the per-asset `related` argument from `insert_assets`. It only controlled task-scope inclusion and was not stored; re-registering an asset invalidated the decision, and the UI could not show which assets were considered unrelated.
- **Test scope (`task_scope`) is independent of the asset coverage switch**: `insert_assets` automatically adds scope (`source='auto'`) regardless of coverage, and `add_task_scope` is always available to Planner, task Main agent, and goal decomposition. Scope defines authorisation boundaries and asset-query filters; the coverage switch only determines whether it is used as a metric denominator. Previously disabling coverage disabled both `auto` and `agent` writes, leaving only manual UI entries. `list_untested_assets` remains hidden when coverage is disabled because it is exclusively a coverage view.

#### Fixed

- **Fixed assets leaking between tasks** (#59): `list_assets` previously hard-coded task ID 0 and queried the shared asset database without a usable model scope filter, causing other tasks' assets to be mistaken for current targets. It now returns assets within the `task_scope` of **the current task and its directly linked tasks**, matching by attribution: root domains include their subdomains / services / endpoints, and networks include their hosts / services. Direct ID lookup also excludes out-of-scope assets. Non-task contexts (Auto/pentest) still fall back to the full database. This also fixes network matching for direct-IP hosts such as `http://1.2.3.4/api`. The UI Test assets view, filtered by task producer, is unchanged.
- **Fixed Worker trace review tools being removed accidentally**: a legacy startup migration unbound `search_all_worker_traces`, `get_worker_trace`, and `node_detail` after they were added to the default tools. They are now excluded from that migration and rebound once in databases where it already ran.
- **Fixed intermittent `/btw` side-question persistence failures**: strips JSONB-unsupported NUL (`\u0000`) escapes before saving side_question checkpoints.

### Triggers

#### Fixed

- **Merged trigger sessions deduplicate task descriptions / goals per task**: avoids repeating long goals when multiple tasks trigger together.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.9] - 2026-09-11

### Agent

#### Added

- Task Main agents support **multiple sessions**, each with independent context reset and interaction.
- Persistent `/btw` **side questions** leave the main conversation uninterrupted and survive restarts.
- `spawn_task` adds `source_task_ids`, allowing new tasks to **inherit read-only** assets and findings from source tasks.

#### Changed

- Disabled cross-engagement Worker memory and reduced default tools: context reading and review across work runs are planning responsibilities; Workers execute and write back one intent.
- Simplified default Worker prompts; removed `terminated` and `worker_name` from `get_worker_output`; shortened `insert_assets` / `list_assets` descriptions.

#### Fixed

- Fixed work ending prematurely after **idle turns**: models sometimes emit only reasoning, with neither body text nor tools. The harness treated this as natural completion (`end_turn` without `tool_use`) and returned `completed` with an empty summary despite unfinished work. None of the five LLM retry layers covered it: it is not an error, safe-window retry requires stream failure, the circuit breaker treats `err == nil` as success, intent reruns require `model_error`, and SDK empty-response checks count reasoning events as output. The work Stop hook now detects these turns and injects a continuation message, preserving generated reasoning for the next step. It deliberately avoids identical replay because this behaviour is often determined by prompt and context structure rather than random failure.
- Uses **empty-response retry count** from LLM "Retries and backoff" settings, as both address completed model calls without substantive output. Default 2; `-1` disables it and restores ending on an idle turn. This is a **total per-intent** limit, not a consecutive count: the harness only nudges once during consecutive idle turns and resets that allowance after an actual tool turn. It limits repeated tool → idle → nudge → tool → idle cycles that consume the intent budget. Trigger logs retain the original message `[work <worker> · #<意图>] 空转回合…注入续跑指令 (n/N)` (idle turn, continuation instruction injected). Design: `docs/LLM重试设计.md` §1.1.
- Fixed `/btw` context budgets and input layout in long conversations; request IDs fall back to an alternative generator outside secure contexts (non-HTTPS).

### Traffic

#### Added

- Findings support **multiple linked traffic evidence entries**, ordered, annotated, and labelled by role (request / response / supporting evidence).
- The report Agent **automatically links** relevant traffic evidence before writing a report.
- Added an Agent **traffic linking switch** and completed evidence handoff between Agents.

### Tasks

#### Added

- **Session-level finding retests** run in independent Agent sessions, with run status in lists and details.

### Interception

#### Added

- Approval records add **details and execution audits**, including tool request context, initial model / rule decisions, execution output, and parameter fingerprints.

### Assets

#### Added

- Task test assets support **DSL search**.

### LLM

#### Fixed

- Fixed connection tests omitting custom session headers and causing opencode zen to return 400.

### Network

#### Added

- MCP HTTP transport can **skip TLS certificate verification** for self-signed services.

### UI

#### Added

- Conversations are grouped by Agent with expand/collapse and independent pinning; chats can be filtered by Agent.
- The attack-chain graph renders digest nodes and folds their members.

#### Fixed

- Fixed blank pages caused by unsynchronised login credentials.
- Interception notices explicitly identify "Platform controls" to avoid confusion with target-side defences.

### Deployment and updates

#### Added

- **One-click updates from the page**: a new Version and updates card in system settings and a top-bar notice. Downloads the release → checks `SHA256SUMS` → runs a smoke test → stages it → exits for the supervisor script to restart and replace it; the page refreshes automatically.
- New supervisor launch scripts `start.sh` / `start.bat` are the official entry points, included in releases and Docker (`install.sh` is unchanged). They restart based on exit code and forward SIGTERM to artex. Verification and replacement logic stays in Go; scripts remain simple.
- Automatic fallback: failed verification or smoke tests discard the update and keep the current version; three consecutive startup failures roll back to the previous version. Settings also support manual rollback (database schema is not rolled back).
- Updates require GitHub domains and HTTPS; the source is fixed. Development builds disable one-click updates. GitHub queries are cached for 30 minutes to avoid exhausting the API quota.

#### Known limitations

- Docker updates replace the program, not the image: the toolchain stays unchanged, and container recreation restores the image's version. Use `docker compose pull artex` when needed.
- Release `skills/` contents are not synchronised; newly bundled skills do not activate automatically.
- Updating restarts the service and interrupts running tasks.

### Dependencies

#### Changed

- Upgraded norma to v0.3.7.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.8] - 2026-09-09

### LLM

#### Added

- New LLM "Retries and backoff" tab configures **counts** and **intervals** across five retry layers, from inner to outer: connection retry (SDK, pre-stream resets / timeouts / 429 / 5xx), empty-response retry (SDK, normal completion without content, OpenAI format only), safe-window retry on the same provider (replay before any output reaches the caller), pool circuit breaking (cooldown after repeated failures), and intent rerun (Worker `model_error`). Outer layers start after inner layers are exhausted. Each layer has two settings: blank retains default count and exponential backoff; a count overrides the count; an interval replaces exponential backoff with a fixed delay; -1 disables that layer. The first three follow endpoints and support per-field profile overrides of global defaults. Circuit breaking and intent reruns are process-wide global settings. Saves apply immediately. All-blank settings preserve existing behaviour, including upgraded databases (new columns default to 0; missing settings use defaults). Connection tests omit these settings because their hard 30s timeout could misclassify working endpoints. Design: `docs/LLM重试设计.md`.
- Each LLM profile supports a custom **session header** (`session_header_key`): when set, requests include this HTTP header with the current session ID (`conv-<id>` for chat, `exp<x>-worker-i<intent>` for Workers, etc.), supporting gateway prompt caching / sticky routing. IDs are injected from request context without norma changes, so shared providers can send distinct session values. Includes migration and a new profile-dialog field.

#### Fixed

- Fixed saving profiles with an empty session header causing `23502`: the column is `NOT NULL DEFAULT ''`, but `NULLIF($n,'')` incorrectly wrote NULL. Empty strings now pass through directly.

### Agent

#### Added

- Wall-clock timeout now **finishes in place**: with norma v0.3.6, `MaxDuration` interrupts active tools and runs configured wrap-up rounds on a live context to save identified content and a summary, ending as timeout. It replaces Worker/Planner `maxDur+90s` hard contexts that killed stuck runs as `aborted_tools`. Chat receives the same harness behaviour; Main agent has no `MaxDuration` and is unaffected.
- Stall fallback: When the planner wakes on a heartbeat or no-change event and the entire graph has no open or running intents, its opening changes to a stall warning. It explicitly states that no workers are running and no directions are queued. That round must produce one or more distinct new intents, never zero.

#### Changed

- Worker prompt restructuring: intent / launch instructions / anchored-asset raw JSON move into the system prompt, rebuilt every round and never compacted away. Resuming no longer depends on preserving the first transcript message. The initial user message contains only the global overview, which tolerates fallback and stale data. Per-intent system data sacrifices cross-intent cache reuse to preserve intents reliably.
- Simplified Planner / Worker defaults and addressed practical issues: the planner distinguishes `recent_done` by status (inspect the trace before deciding on blocked/exhausted, neither treating them as dead ends nor rerunning them blindly); negative conclusions are observations or tentative readings rather than final verdicts, with evidence checked before adoption; when the goal is unmet and no intents are open/running, it must produce intents; depth takes priority over coverage. Workers write negative conclusions only as observations with tentative interpretations, leaving the decision to the planner. They put clues relevant to other intents in the fact summary for the planner instead of pursuing them directly. Reseed appends the new defaults as a new version and switches to it; user customizations and older versions remain in history for rollback.
- Reduced default work-agent tools and asset writeback guidance: Workers execute and write back single intents; context reading and cross-work review belong to planning. Removed `list_facts`, `node_detail`, `list_companies`, `search_all_worker_traces`, `list_worker_traces`, and `get_worker_trace` from defaults. Kept `list_findings` for deduplication, `add_finding`, `record_fact`, `insert_assets`, and `list_assets`. Removed obsolete prompt fields conflicting with the `insert_assets` schema (`type=tech`/`on_url`/`props`). A one-time migration removes Worker bindings without changing Planner/Main bindings.

### Exploration graph

#### Added

- **Exploration graph cold-node compaction (cold-digest)**: folds old, inactive intents / facts into digest nodes in `graph_overview`, preserving original nodes permanently for complete ID-based restoration. Storage is lossless and folding reversible. Classification uses reverse reachability, marks nodes hot if any branch is active, applies R=6-round debounce, and groups connected components. Background minor compaction folds uncovered cold blocks; major compaction recompresses sources and merges fragments. Activity rechecks and cooldown exclusion prevent hot-path work or overwriting reactivated nodes. Overviews provide `cold_digests` and asset-indexed `cold_index`; `expand_digest` / `expand_index` restore detail. Linked / inherited tasks reuse their own folded views; `expand_digest` supports cross-task read-only restoration. Expansion tools are available to Planner and Main agent, not Worker. Includes migrations.
- `graph_overview` returns the complete `finding_list`, as confirmed findings are valuable and typically few per task. Planner/Worker rounds see all confirmed findings without calling `list_findings`, unlike the recent fact window. Entries use `{id, summary, evidence?, from_intent?, assets?}`, with readable asset URLs / domains / ip:port rather than bare IDs.

#### Changed

- `graph_overview` no longer lists `hosts`: coverage retains `host_count` but omits up to 500 host strings per round; details remain available through `list_assets`. Added top-level `done_intents_total` alongside `recent_done_intents` limited to ≤15 so Planners can tell whether history was truncated.

### Tools

#### Changed

- Default `add_company_scope` binding moves from Worker to Planner, since company scope definition belongs to planning / main control / Auto. New databases bind mainagent/planner/auto; existing databases receive a one-time migration.
- Planner now binds `list_assets` by default, alongside `list_untested_assets`. Existing databases receive a one-time backfill without overriding user unbinding.

### Tasks

#### Added

- New "Running Workers" task column counts `state='running'` intent nodes, matching task Overview. Shows 0 when none are running.

### Network

#### Added

- New **global egress proxy** (http/https/socks5, supporting `user:pass`): with traffic capture enabled, it becomes the MITM recorder's upstream for both intercepted and passthrough traffic, retaining recordings without exposing the source IP. With capture disabled, it is injected into Agent bash environments and WebFetch (`proxyEnv` adds `ALL_PROXY` for socks5). Stored in settings KV without migration, with a Global proxy card separate from web-search and LLM proxies.

### UI

#### Fixed

- Corrected intent status labels: `exhausted` changes from "Exhausted" to "Budget exhausted" (step/time budget interruption with partial results, not an exhausted direction); `blocked` changes from "Blocked" to "Execution error" (model / API / network retry exhaustion, not target / WAF interception). Added missing `stopped` label "Stopped" for manually stopped work.

### Dependencies

#### Changed

- Upgraded norma to v0.3.4 for MCP output truncation and disk capture (`651b961`); v0.3.6 later enables timeout wrap-up in place.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.7] - 2026-08-31

### LLM

#### Added

- Model profiles add an **output limit** and request field selector. It limits generated reply tokens and is sent with each request; 0 (default) omits it and uses server defaults. Context window is separate: total model capacity used locally for compaction, never sent. The selector only applies to `openai` Chat Completions: blank uses `max_tokens` for most compatible gateways; official reasoning models (o series / GPT-5) require `max_completion_tokens` and reject `max_tokens` with `unsupported_parameter`. Anthropic uses fixed `max_tokens`; Responses API uses `max_output_tokens`, so the selector is disabled and cleared for other formats. Output limits now connect to Planner / Worker / chat / Main agent / goal decomposition, which previously omitted them (Anthropic used SDK fallback 8192). Values resolve each round like streaming settings and apply after failover. Existing database behaviour is preserved through default 0 and blank field names.
- Backend connection tests log each HTTP attempt's status and original gateway response, limited to 4K, to diagnose 401, quota messages, empty frames, and HTML responses beyond the UI's folded ok / err. Independent of LLM recording settings.

#### Changed

- Task LLM chains can be edited in any state, including done / failed / timeout, since Main agent conversations still use them after task completion. Removed terminal-state restrictions in HTTP and database transactions and enabled dialog editing. Saving a terminal task no longer reopens quota-blocked intents, which would otherwise become open without Workers or eligibility for rerun. Intent rerun / new goals still reactivate tasks explicitly.

### Agent

#### Changed

- Messaging a running Worker now reuses pause / resume and transcript resume, matching Main agent conversations. This replaces a custom intervention protocol affecting scheduler barriers, recovery, and many activity filters. Messages enter the next input round; intents run in a dedicated goroutine outside the three-slot pool. The UI retains the message input and restores "Continue directly"; SSE carries messages. No schema change. Messages exist only in memory and cannot recover after crashes; sending may briefly exceed task concurrency by one Worker, acceptable for this infrequent action.

### Tasks

#### Fixed

- Fixed OOM when archiving large tasks: snapshots stream instead of loading entire tasks into memory. Also repaired three cold-archive recovery gaps: modern traffic exists only in SQLite, and a crash between PostgreSQL and SQLite commits could strand archived traffic in hot storage. A staging journal is now always written regardless of historical directories; missing `journal.json` is treated as discardable.
- Fixed slow UI and conversation loading with many tasks: optimised task list, context, and exploration queries and reduced duplicate dashboard, conversation, and task-detail requests.

### Assets

#### Changed

- Removed the 256-rule company scope limit from frontend, backend, and demo mocks. Individual rules still have a 1024-character cap; the 2 MiB request limit remains, allowing roughly 40,000–50,000 rules.

### Skills

#### Fixed

- Fixed skill uploads failing with "Upload failed: zip: unsupported compression": Go's standard library only decompresses Store / Deflate. Added pure-Go bzip2 and Zstandard support without external dependencies. Unsupported Deflate64 / LZMA / XZ / PPMd and encrypted archives are rejected before extraction with a clear message identifying the file, compression method, and repacking instructions instead of raw errors.
- Fixed Chinese skill filenames being rejected by the ASCII `[A-Za-z0-9-_./]` path allowlist. A Unicode blocklist now permits filenames in all languages and spaces while rejecting control characters, invalid UTF-8, zero-width / bidirectional controls, `\ % # ? * : " < > |`, `..`, absolute paths, and empty segments. Traversal protection remains. Skill names still restrict ASCII to lowercase letters, digits, and hyphens (agentskills.io), while allowing non-ASCII letters such as Chinese; spaces, dots, and separators remain forbidden. Example filename `参考/说明.md` is preserved.
- Fixed garbled or rejected Chinese filenames in Windows ZIPs without the UTF-8 flag: decode with GBK fallback before path validation. Quoted frontmatter such as `name: "中文技能"` now yields the correct skill name.

### UI

#### Added

- New default **All findings** flat view, switched with the existing By task view through header tabs. A cross-task table offers 10/20/50/100 entries per page, with the same selection/export, inline evidence/report expansion, name/type/severity edits, disposition changes, follow-up, and deletion controls. Views share statistics and filters, preserving them across switches and locally persisting the view. Only the active view polls; inline edits update both caches.
- New **By asset** findings view: left asset tree (company → root domain / IP → subdomain → service → endpoint) and right table for the selected subtree, sharing tables and filters with other views. Only assets with findings appear; missing ancestors are reconstructed. Counts aggregate and deduplicate findings per subtree. Deleted or unlinked assets enter "Unlinked assets". This view queries on entry, filter changes, finding edits, or manual tree refresh, rather than polling every 5 seconds. Each tree level shows only its relative part (subdomain without root suffix, `https :443`, endpoint path), with full values on hover and breadcrumbs. If too many nodes exist, endpoint/service levels are omitted with a notice while counts remain in ancestors.

#### Fixed

- Fixed clipped conversation history on mobile: narrow screens collapse the conversation list to give history more width.

### Dependencies

#### Changed

- Upgraded norma v0.3.2 → v0.3.3: `Config.MaxTokensField` lets OpenAI Chat Completions output limits use `max_completion_tokens`, required by reasoning models. The keys are mutually exclusive; only one is sent, with `max_tokens` remaining the default.
- Upgraded `golang.org/x/mod` v0.37.0 → v0.40.0, fixing two dependency security alerts.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)
- [@begininvoke](https://github.com/begininvoke)

## [0.3.6] - 2026-08-27

### LLM

#### Fixed

- Fixed the non-streaming switch reverting to streaming after reopening (#69): the profile-list DTO omitted `streaming`, so the frontend treated `undefined` as the streaming default. Writes and database storage were correct. The DTO now includes the field without `omitempty`, ensuring `false` is returned.
- Connection tests now verify actual model replies and use the profile's configured streaming mode (#65). Previously HTTP success was sufficient even when reasoning consumed the budget, safety policy removed body text, or a compatibility layer dropped `content`. Tests also always streamed, testing a different path for non-streaming profiles. Empty replies now fail; successful notices show the reply, with the current streaming setting included in the test.

### Agent

#### Changed

- `list_facts` uses pagination and keyword filters to prevent context overflow (#74): latest 20 by default, `limit` up to 100, `before` cursor, and summary keyword `q`, returning `{facts, total, has_more, next_before}`. Long summaries are character-truncated; `node_detail` retains full text. Worker / Planner prompts reflect pagination. A one-time migration updates the tool catalogue schema because `SeedTool` only inserts initially; otherwise tool management displayed "No parameters".

### Tools

#### Added

- New tool-call statistics on Tool execution (#72): the Statistics button shows calls, percentage, and failures by tool, descending by count. It uses current task / keyword filters across the full result set, and fetches only when opened.

### Dependencies

#### Changed

- Upgraded norma v0.3.1 → v0.3.2: restores silently discarded OpenAI reasoning/refusal (`reasoning_text` in Responses), deduplicates three reasoning field aliases, adds bounded retries for responses with zero content blocks, and fixes compaction splitting tool pairs and causing gateway 400 errors, including existing transcript orphans.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.5] - 2026-08-25

### LLM

#### Added

- Each LLM profile supports streaming / non-streaming (streaming by default): enabled uses SSE; disabled sends `stream:false` and receives one complete JSON response, avoiding broken gateway SSE implementations at the cost of live progress and token counts. Worker / Planner / Main agent / chat / goals resolve the active profile dynamically. `llmpool`, `llmrec`, and task runtime provider wrappers support non-streaming. The new `streaming` column in `llm_profiles` is added to old databases with `ALTER`, defaulting to `true`.
- LLM profiles support OpenAI Responses API as the third format `openai-responses`, sending `POST /v1/responses` alongside Chat Completions / Anthropic. Includes `BaseURL` normalisation, default model `gpt-5`, an idempotent migration extending the `format` constraint in `llm_profiles`, and the frontend option "OpenAI (Responses API)". Requires norma v0.3.1, including the `reasoning_content` return fix.
- Records original LLM HTTP request / response bodies at the transport layer, retaining tool schemas, `tool_use` blocks, and raw SSE frames absent from normalised views. Each norma internal retry is recorded separately. Adds `raw_request` / `raw_response` to `llm_records` with `ALTER` migration; details include a Raw view and copy buttons with `execCommand` fallback outside secure contexts.

### Conversations

#### Changed

- Conversation selection becomes a **Select multiple** mode: checkboxes are hidden normally, with "N total + Select multiple" in the header. Selection mode shows checkboxes, select all, and bulk deletion; Done exits and clears selection. Complete bulk success exits automatically; failures remain selected for retry. Individual rename / pin / delete remain in row menus.

### UI

#### Changed

- Improved task management, conversation controls, and traffic views (#57): persistent task-column sorting, direct rename icons instead of menus, and refined sheet and traffic interactions.

#### Fixed

- Worker asset labels show only domains and IPs and handle empty labels correctly.

### Agent

#### Added

- Added quantitative acceptance checks to Planner goal decisions: When a goal has measurable acceptance criteria (asset test coverage reaches X%, N flags are obtained, or a certain permission is gained), the planner must compare actual `graph_overview` values (`coverage.pct`, finding counts, and so on) before `prove_goal`. If the measured result falls short, it must dispatch intents to close the gap and may not mark the goal met because it is "mostly complete." This fixes a case where a 100% coverage goal was judged complete with only 40% measured coverage.

#### Changed

- Revised Planner criteria for producing zero intents in a round: The previous wording described zero intents as the "most common and important principle," which could cause the planner to stop early while the goal was unmet and in-scope areas remained untested. Zero intents now apply only when (1) every identified direction is already covered by open/running/recent_done intents, or (2) the next step depends on output from currently running work that has not appeared yet, so planning should wait for that work to finish and the graph to update. Conversely, an uncovered direction independent of running work, or an unmet goal with untested in-scope areas, should not be dropped merely because zero intents were described as common.
- Strengthened evidence thresholds for Worker negative conclusions: Before concluding "not injectable," "port closed," "no login entry," or another negative result that might cause the planner to abandon a direction, a worker must exhaust reasonable methods within that intent (vary encoding, parameters, paths, or methods). If those methods remain untried or evidence is weak, mark `confidence=inferred`. This prevents a premature `observed` negative from closing a whole route, especially early in a task when a mistaken negative can redirect work and be hard to correct.
- The Planner / Worker default prompt changes above are appended as new current versions through one-time `reseedPlannerPrompt` / `reseedWorkerPrompt` migrations guarded by settings flags. Older versions remain in history and can restore customised prompts.

### Operations

#### Added

- New `reset-password.sh` resets the fixed `ARTEX` administrator password in local / Docker deployments. Connection details can be explicit or read from `--dsn` / `$ARTEX_PG_DSN` / `config.json`. Uses database `pgcrypto` to create a login-compatible bcrypt hash in `settings.auth.password_hash`; no restart is needed. Passwords pass through environment variables rather than process argv and are escaped against injection.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

## [0.3.4] - 2026-08-24

### UI

#### Added

- Selected tasks support bulk category changes. "Uncategorised" removes the current category. The batch runs in one transaction; only tasks deleted after selection can fail.

#### Changed

- New-task category selection uses a searchable input instead of a dropdown. Typing filters existing categories; Enter or Create immediately creates and selects a new name. A removable tag displays the selection; only one category is allowed.

### Conversations

#### Fixed

- Fixed messaging being blocked by "LLM not configured; chat unavailable" when a conversation had a selected profile but none was globally active. The pre-send check now shares execution's resolution rules: conversation / Agent binding first, global fallback second.
- Unavailable-chat notices now distinguish no profiles (Add profile) from inactive profiles (Activate one or select a profile for this conversation), instead of always saying "Not configured".

### Agent

#### Added

- For tasks with all goals met, Workers can directly claim Main agent `add_intent` requests and stop when finished. Planner stays inactive without open goals, avoiding cancellation after re-evaluating already met goals. The task returns to completed when the frontier empties. Before issuing an intent, Main agent asks whether it should become a formal goal; doing so restores normal autonomous planning.

#### Changed

- `get_worker_trace` / `get_task_worker_trace` no longer reject `step_ids` exceeding the five-step limit. They return the first five complete steps and identify returned and omitted IDs through `returned_step_ids`, `omitted_step_ids`, and `notice`, explaining that no further retrieval is needed if sufficient. Duplicate and invalid IDs are removed before counting.

#### Fixed

- Task lists now sort by descending task ID instead of creation time. Simultaneous tasks shared timestamps and unstable ordering, which combined with 10-second polling and random map iteration caused rows to exchange positions repeatedly.

### Assets

#### Fixed

- Fixed company attribution recalculation failing on one invalid row: hostnames stored in `assets.ip` made `a.ip::inet` throw `22P02`, rolling back company scope creation / modification and company deletion. Safe `try_inet()` conversion now skips invalid values instead of aborting the statement.
- Unparseable `ip` values are no longer silently skipped. Saving company scope reports the count, IDs, and values of invalid assets, explaining they cannot match IP/CIDR attribution. Server logs also cover paths without a frontend response, including company deletion, scopesentry sync, and Agent writes.
- Task IP/CIDR scope matching also uses `try_inet()`, replacing character-only regex guards that let hexadecimal-letter hostnames such as `abc.def` trigger the same error.

#### Changed

- The `ip` field of `ip`, `service`, and `endpoint` assets no longer accepts hostnames. `insert_assets` and asset APIs return per-entry errors with `index` and correction guidance: use `type=subdomain` with `domain`, or resolve A/AAAA records first. Agents can correct and reinsert; other batch entries are stored normally.

### Contributors

- [@neouks](https://github.com/neouks)

## [0.3.3] - 2026-08-23

### Worker

#### Added

- Running Workers support individual pause, resume, and cancellation. Pause retains intents, facts, and findings; cancellation transactionally cleans the intent and its direct outputs after execution exits.
- Added `paused` intent state, execution fences, and named termination causes to prevent late blackboard writes after pause, cancellation, or task deletion.
- Added an optional task concurrency limit; creation, resume, finding follow-up, and queue replenishment use a persistent FIFO admission path.

#### Changed

- Default Worker wall-clock duration increases from 600 to 1200 seconds. Reactivated timed-out tasks restart the clock on their next actual execution.
- Real Worker, Planner, and Main agent execution states drive task badges consistently, fixing Idle shown while Workers run.
- Worker sessions retain individual controls and current-session token totals; model names move to the right of the current session title.

#### Removed

- Removed Worker multi-selection, select all, and bulk pause / resume UI, along with bulk control APIs and mock contracts.
- Removed row-level Worker token badges and tooltips; the complete usage ledger and task aggregation APIs remain.

### LLM

#### Added

- Tasks support ordered LLM profile chains. Explicit provider quota exhaustion switches to the next profile and persists the active profile, exhausted state, and error summary.
- Running and paused tasks support full chain editing, reordering, and manual switching, effective from the next LLM call.
- Automatic failover, manual switching, and full-chain exhaustion produce structured system activity and deduplicated task-stream notices.
- Added task-role model resolution APIs for the profile and model used by the next Main agent, Planner, and Worker calls.
- Added an optional global LLM Pool with call ordering, designated-model fallback, health status, cooldown recovery, and manual reset.
- Added an always-enabled LLM ledger aggregating input, output, and cache tokens by task, session, model, and profile.

#### Changed

- Goal decomposition, Planner, Worker, and Main agent share task-level LLM runtime (resolution order below).
- Failover responds only to explicit quota, balance, or billing errors; ordinary rate-limit, authentication, network, server, and context errors do not cause incorrect switching.
- LLM settings use profile cards and a right-side editing drawer, with Pool rotation order, priorities, exclusions, health, and recovery controls.
- Chain-exhaustion notices wrap on narrow screens. The current model moves from a conversation-list icon to a label beside the session title with a full tooltip.
- Task-role LLM resolution becomes Agent binding → task chain → global. Explicit model bindings always take priority; unbound roles use the chain, falling back globally when the chain is empty.
- An empty outbound proxy means direct connection, without falling back to `HTTP_PROXY`/`HTTPS_PROXY` (explicit `ARTEX_LLM_PROXY` is unaffected). Supports authenticated `socks5://user:pass@host:port`.

#### Fixed

- Transient streaming failures before any output are retried on the same provider with safe backoff, reducing interruptions after a few tools with no summary and terminal `model_error`. Quota exhaustion, excessive context, and deterministic 4xx errors are not retried; failover and compaction recovery handle them as before.

#### Removed

- Removed conversation-list LLM icons to avoid confusion with Worker status icons.

### UI

#### Added

- Tasks support global category creation, rename, deletion, and filtering; categories can be selected at creation.
- Added global task-template CRUD and a right-side management drawer. New tasks can load preset descriptions / goals or save current input as a template.
- New tasks can link multiple source tasks and company asset scopes. One multiline field recognises domains, URLs, IPs, CIDR, ICP, and company keywords.
- Task test assets can be added / removed live, recording manual, company, inherited, and Agent-discovered sources. Worker session titles show current test assets and source summaries.
- Findings are grouped by task with independent group pagination; finding descriptions can create high-priority follow-up intents.
- Conversations support rename, pin, unpin, and delete; task lists support current-page bulk pause / resume.
- Traffic and tool-execution details use right-side drawers. HTTP messages include `Host` and highlight request lines, status codes, headers, JSON, and markup bodies.
- Task action columns add Details and Pause / Resume. Send shortcuts are configurable (`Enter`, `Cmd+Enter`, etc.) in system settings; web search adds a proxy field.
- The session-title badge shows the LLM profile name, with model ID in the hover tooltip. Tasks can have optional names, with a new name column and search; empty names fall back to descriptions.
- Skills show usage counts, last-used time, and missing dependencies to help identify inactive or uninstalled skills.

#### Changed

- Task titles become focusable detail links. Task assets, company assets, finding task groups, and group findings use real server pagination, stable ordering, and exact totals.
- Company creation uses a right-side drawer with multiline scope entry and live recognition, validation, and type previews.
- Main agent input grows automatically, sends with `Enter`, inserts a line with `Shift+Enter`, and prevents accidental sending during Chinese IME composition.
- Agent previews, task reports, and detail views share Markdown rendering. Fixed deletion confirmations, long errors, and mobile drawer widths consistently.
- Template selectors display/search by name and submit by ID. Restored original system font sizes and consistently display application version `0.3.3`.
- Task and current-session token totals highlight only input, cache reads, and output. Send uses a simpler up-arrow icon.
- Task Overview test scope displays company names instead of IDs.

#### Fixed

- Ordinary dotted text is no longer misclassified as an ICP registration number.
- Fixed variable-catalogue names colliding with global runtime variables such as `{{.Now}}`, producing duplicate keys in the Agent editor variable list.

#### Removed

- Removed separate Enter buttons from task cards; clicking the title opens details.
- Removed the dashboard New task button, company Logo URL input, and finding count badges from the Findings page and task groups.
- Reverted the global 10% font increase and removed Worker checkboxes, model icons, and row token statistics from conversation lists.

### Agent

#### Added

- New tasks can link multiple existing tasks and inherit direct-source goals, facts, findings, completed intents, asset scope, and blackboard context live and read-only.
- Blackboard read tools query source-task nodes, facts, findings, and traces on demand. Inherited nodes carry source labels and all write tools reject modification.
- Company scope tools support domains, URLs, IPs, CIDR, ICP, and company keywords. Keywords guide Agent scope only and do not determine automatic attribution.
- Finding follow-up creates a high-priority manual Worker intent in the original task with asset anchors and a `derived_from` edge.
- Overview adds Goal management for manual viewing, creation, editing, and deletion. Creation / editing notifies Planner and reactivates tasks. Deletion removes goal nodes and cascades edges / anchors without reactivation.
- Main agent receives `steer_work` by default for live Worker correction without interruption or lost progress, validating that the intent belongs to the task.

#### Changed

- Source-task intents do not enter new-task frontiers; new tasks retain independent explorations, queues, directories, and conversation histories.
- Deleting running intents preserves data: stops them as `stopped`, requires a reason, records it as an attached fact and in payload, and triggers Planner through `cancelled`, retaining intent content and reason.
- Main agent conversations use server activity streams; failed sends restore input. Opening a session scrolls to the bottom and keeps the latest reply visible after detail lazy-loading.
- Pausing tasks terminates current Main agent, Planner, and Worker calls but allows users to start new Main agent orchestration sessions while paused.
- Cancellation, shutdown, and stream interruption retain actual termination causes, generated content, rounds, elapsed time, tokens, and outstanding tool calls.

#### Removed

- Removed optimistic client echo from Main agent chat to prevent duplicates and cross-session streaming during pause, failure, or concurrent activity.

### Constraints

#### Added

- Goal decomposition extracts operational allow/deny constraints before goals, with Main agent able to supplement them at runtime. Constraints enter Planner and Worker system prompts at highest priority; diversity and wider exploration remain explicitly subject to them.
- Overview adds Constraint management and create/update/delete APIs. Constraint injection can be enabled separately for Planner / Worker (both on by default), reread each round for immediate effect. `task_constraints` uses `CREATE TABLE IF NOT EXISTS` to populate older databases automatically.

### Interception

#### Added

- Command interception adds model fallback approval beyond regex/string rules. When no rule matches, the model returns `ALLOW`/`ASK`/`DENY`; failure and approval-timeout actions are configurable. The page separates Interception rules / Model profile tabs, and model decisions show a `[Model]` prefix and reason.

#### Fixed

- Hardened judge output parsing to prevent correct `DENY` decisions being treated as approval.

### Traffic

#### Changed

- Traffic recording stores entire exchanges in SQLite, with large bodies in hash-deduplicated blob storage. Text bodies use trigram full-text indexing for arbitrary substrings and Chinese search. Deletion becomes one SQL transaction, reducing hours to milliseconds without stopping recording. Added streaming downloads for large bodies and full-text `traffic_search` parameters. Legacy file-tree data remains readable, searchable, and deletable without migration.

### Tasks and assets

#### Added

- Task creation adds an Asset coverage switch (on by default). Disabling it removes coverage calculation/display, shows only assets in the overview graph, stops automatic `task_scope` accumulation, and removes related Planner / Main agent tools. Company links are unaffected.
- `insert_assets` adds per-asset `related` (default `true`), effective only with coverage enabled. `false` stores assets in the shared database without counting task coverage, such as incidental neighbouring sites or unrelated assets.
- Company scope and task assets support domains, URLs, IPs, CIDR, ICP, and keywords. Domains/IPs create or reuse global assets; CIDR/ICP/keywords remain task-scope context.

### Build

- `build.sh --release` builds Linux amd64/arm64, macOS amd64/arm64, and Windows amd64 together, producing ZIP releases with `skills/`, sample configuration, and README.
- Go linker strips debug information and generates ZIP releases. UPX becomes explicitly optional to avoid startup segmentation faults from self-extracting ELF on some Linux systems.
- Release Workflow smoke-tests Linux amd64 startup and includes `SHA256SUMS` with releases.

### Contributors

- [@neouks](https://github.com/neouks)

## [0.3.2] - 2026-08-20

### Added

- New tasks link multiple existing tasks and inherit direct-source facts, findings, completed intents, asset scope, and blackboard context live and read-only, retaining independent queues, directories, and histories.
- Tasks support ordered LLM profile chains, switching on explicit provider quota exhaustion and persisting the active profile, exhausted state, and structured audit activity.
- Running and paused tasks support chain editing, reordering, and manual switching. Task details report automatic failover, manual switching, and full-chain exhaustion.
- Running Workers support pause, resume, and cancellation. Pause retains blackboard data; cancellation transactionally removes the intent and its directly produced facts, findings, and execution records after writes stop.
- Task deletion can also clear linked assets, traffic, findings, and task files, with deletion barriers, concurrency protection, and auditable deletion statistics.
- Added an optional task concurrency limit. New tasks queue FIFO at the limit and start when slots become available.
- Added task/company asset pagination, category statistics, and paginated intent mock contracts.
- Added single-binary `build.sh` with static frontend export, embedded resources, cross-platform targets, and build-version injection.

### Changed

- Explicit task LLM chains coexist with the global Pool, retaining strict quota-failover semantics. Without an explicit chain, Agent bindings and global rules continue to apply.
- Main agent input supports growing multiline text, `Enter` to send, `Shift+Enter` for a newline, and protection during Chinese IME composition.
- Planner, Worker, and Main agent share task-level LLM runtime. Chains use the smallest candidate context window as the safe compaction threshold.
- Task details show running, idle, and paused states from actual LLM calls. Source facts, findings, intents, asset references, and graph nodes retain source labels and read-only status.
- Agent previews, task reports, and detail views share a Markdown renderer.
- LLM profiles use cards and drawers, with independent scrolling in model lists.
- Task pause no longer blocks Main agent chat. Orchestration sessions are independent; new messages can be sent while paused, which only terminates the currently active round.
- Default Worker run wall-clock duration increases from 600 to 1200 seconds.

### Fixed

- Removed optimistic Main agent console echo in favour of server-rendered messages, fixing mixed or cross-session content after pause or failed sends.
- Main agent sessions open at the bottom and keep the full latest reply visible after lazy-loading its content.
- Fixed Main agent continuing after task pause and orchestration Agents pausing tasks without stopping Main agent.
- Fixed task badges and action buttons disagreeing with completion or real Planner, Worker, and Main agent execution states.
- Fixed late blackboard writes and leftover files during Worker cancellation, task deletion, and concurrent writes.
- Fixed Markdown preview failures, overflowing deletion confirmations, mobile widths, and misaligned task-detail buttons.
- Added named termination causes to every Agent cancellation path. Activity details show cancelling party, terminal state, rounds, duration, token usage, and outstanding tool calls.
- Fixed a shutdown race where the parent context overwrote the named `shutdown` cause, and garbled Chinese activity summaries caused by byte truncation.
- Fixed cancelled calls with partial streaming output losing their actual termination cause, while preserving generated content.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

[Unreleased]: https://github.com/Autumn-27/ARTEX/compare/v0.3.10...HEAD
[0.3.10]: https://github.com/Autumn-27/ARTEX/compare/v0.3.9...v0.3.10
[0.3.9]: https://github.com/Autumn-27/ARTEX/compare/v0.3.8...v0.3.9
[0.3.8]: https://github.com/Autumn-27/ARTEX/compare/v0.3.7...v0.3.8
[0.3.7]: https://github.com/Autumn-27/ARTEX/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/Autumn-27/ARTEX/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/Autumn-27/ARTEX/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/Autumn-27/ARTEX/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/Autumn-27/ARTEX/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/Autumn-27/ARTEX/compare/v0.3.1...v0.3.2
