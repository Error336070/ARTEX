# Long side conversations and context budgets

Fixed on 2026-09-11. The original implementation counted request JSON characters as tokens and inherited the main task's 32K output reservation, prematurely rejecting ordinary side questions with substantial HTML, JavaScript, or tool results.

## Review of open source implementations

- [Grok CLI side context](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/agent.ts#L739): excerpts recent user/assistant text with a roughly 2000-character budget and at most 400 characters per message. This path does not maintain continuous side question history.
- [Grok CLI independent requests](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/utils/side-question.ts): independent cancellation; a 2048-token output cap where supported; no tools.
- [Grok CLI main session compression](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/compaction.ts): estimates tokens, preserves recent content, updates previous summaries with new material, and handles truncation across turns.
- [OpenCode session compression](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/compaction.ts): estimates complete requests, reserves output/buffer space, retains recent content with a rolling summary, and requests summaries without tools. Its defaults are 8000 recent tokens and 4096 summary output tokens.
- [OpenCode overflow recovery](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/runner/llm.ts): recovery runs only before assistant output begins; the recovered call cannot enter the same recovery path again.

ARTEX adopts independent output budgets, recent content plus rolling summaries, and bounded recovery. It preserves norma v0.3.6 structured messages and tool pairing rather than Grok text excerpts, and does not write OpenCode-style main-session compression events to the ARTEX main transcript.

## Request budgets and execution

- Messages use norma's UTF-8 byte estimate per content block with a 4/3 safety factor, plus system prompts, tool schemas, and message framing. This is not an exact model token count.
- Side answers default to at most 8192 tokens and do not exceed the main profile's configured output limit. `ARTEX_BTW_MAX_OUTPUT_TOKENS` sets a service limit from 256 to 32768 without changing the default model or main task parameters.
- Input budget = context window minus output limit and safety margin. Unknown windows use the platform 200K default. The margin is 5% of the window, bounded to 128–8192 tokens.
- Successful exchanges load in ascending ordinal batches of at most 20. At most 20 full exchanges are retained, with a token budget of at most 1/4 of the input budget and no more than 16K.
- Excess exchanges update the rolling summary, retaining historical sources and context timestamps. Historical assistant answers are not new tool evidence; the latest main snapshot takes precedence on conflicts.
- If main context remains too large, summarize only older messages in its copy, retaining at most 8K recent tokens. Cuts never split tool calls from results; oversized groups are summarized as a whole.
- Summary input is split on UTF-8 boundaries according to the remaining window, with a 2048-token output cap. Empty, truncated, tool-bearing, or over-budget summaries are not cached. At most 12 summary calls share the same 120-second timeout per side request; reaching the cap fails explicitly instead of looping indefinitely.
- An initial provider context overflow before text/tool output permits one retry after further reduction. Recovery stops immediately if the estimated size does not decrease. Other model errors and partial streams do not trigger recovery.
- All obtained usage, including summaries, failed attempts, and cancellation, accumulates on the same side request. Missing Provider usage is recorded as zero; estimates are never presented as measured usage.

## Persistence and interface

`side_question_sessions.memory` stores older exchange summaries, their covered ordinal, and a main context summary cached by snapshot identity. `side_question_requests.context_info` stores preparation phase, actual replay count, summary use, and budget estimates.

Summaries are saved only while the admitted request is running and its clear generation matches. Clear removes caches, and late writes cannot restore cleared data. New snapshots do not reuse old snapshot summaries. Fields are included in v3 task archives; older v3 archives lacking them receive empty objects, while v1/v2 remain compatible.

POST admits and returns the request first. Preparation and compression run in the background without holding the admission lock or a database transaction. SSE/history shows preparation, history summarization, snapshot compression, and answering phases. Compression failures persist as failed terminal states. The frontend retains errors and restores the failed question draft without a toast covering the input; history polling no longer clears submission errors.

## Validation record

- Replay of 19/20/21/50 exchanges, preservation of conclusions beyond 20 exchanges, and summary cache reuse after restart: automated checks passed.
- Long Chinese answers and code context, chunk budgets, tool pairing, immutable snapshots, and cache invalidation for new snapshots: automated checks passed.
- Summary failure/cancellation/truncation/overlength/tool output, clear races, call caps, one overflow retry, and no retry after partial streams: automated checks passed.
- Independent PostgreSQL pagination, restart, v1/v2/v3 archives, restoration of summary caches and budget metadata, old v3 archives lacking new fields, and 20 parent sessions sharing four concurrent slots: passed.
- Candidate Go service build, frontend TypeScript checks, Biome checks for changed components, and Next.js production build in a separate directory: passed.
- In-app browser with an independent UI fixture at 1280×720 and 390×844: preparation phases, summary scope notice, failed draft recovery, no error toast, no horizontal overflow, and no console errors verified. The temporary fixture was removed.
- Read-only replay of existing local Worker snapshots passed the new budget check; for example, Worker #3's 293085-character snapshot was no longer rejected by the local character count. No external model call was made for this check.
- Automatic approval review rejected the real Grok conversation test that would send a private Worker snapshot. It was not run and is not counted as passed.
- At 00:37 on 2026-09-11, the local backend was restarted at the user's request with its existing database, data directory, and login configuration. The running file matched the candidate binary SHA-256; backend and frontend-proxy `/api/health` both returned healthy.

Validation commands (independent test database only):

```sh
go test -race ./sidequestion ./db ./server -run 'TestSide|TestCheckpoint|TestSnapshot|TestBuildRequest|TestService|TestMainSide|TestTaskArchive' -count=1
go build ./cmd/artex
npx tsc --noEmit
npm run build -- --webpack
```

The frontend production build used a separate copy to avoid overwriting the preview's `.next`. The candidate service at `/private/tmp/artex-btw-budget-candidate` was copied to `/private/tmp/artex-btw-preview/artex` and started; the original binary backup is `artex.before-context-budget` in the same directory.
