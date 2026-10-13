# `/btw` validation record

Date: 2026-09-10. Branch: `codex/btw-side-question`. Baseline: `8dae851b9b622f2ff2631f332fde9719d0b16fba`.

Used an independent PostgreSQL test database and data directory. Actual model credentials were injected only into the isolated test environment, never written to code or this record; the product default model was unchanged. Go 1.26.3, norma v0.3.6, Next.js 16.2.9.

Actual conversations, response objects, engineering assertions, and original Qwen review text are preserved in [validation-2026-09-10.json](validation-2026-09-10.json), without API credentials.

## Engineering checks

| Scope | Result | Evidence |
| --- | --- | --- |
| Deep copy of structured messages and tool arguments | Passed | `TestCheckpointDeepCopyAndBoundaries` |
| Summary/compression requests do not overwrite snapshots; complete replies and terminal states publish; partial replies excluded | Passed | `TestCheckpointDeepCopyAndBoundaries`, `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Actual model pool member identity | Passed | `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Tool pairing, 20-exchange replay, budget trimming, overflow errors | Passed | `TestBuildRequestCompactionToolPairingAndBudget` |
| Main/side concurrency and cancellation isolation in both directions | Passed | Blocking Provider, `TestMainSideConcurrencyAndIndependentCancellation` |
| No tool execution, streaming/non-streaming, retained usage on failure | Passed | `TestServiceNoToolsAndUsageOnFailure` |
| Actual norma ChatAgent with local Read tool, main transcript/activity isolation | Passed | `TestSideActualChatCheckpointToolResultAndTranscriptIsolation`, streaming and non-streaming subcases |
| Persistence, pagination, idempotency, retained partial answers after restart | Passed | `TestSideHistoryIdempotencyPagingAndRecovery` |
| Clear versus late-writer races, parent deletion, version comparison | Passed | `TestSideClearLateWritersAndDeletedParent` |
| MainAgent/Worker archive and restore, v1/v2/v3 | Passed | `TestSideTaskArchiveVersions` |
| Three parent API types, authentication, resource ownership, Worker soft deletion | Passed | `TestSideHTTPGlobalLimitTaskWorkerAndDeletion`, `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Side questions while main conversation is busy, independent SSE reconnect/disconnect, cancellation, clear | Passed | `TestSideHTTPBusyIsolationClearAndReconnect` |
| One request per parent / four globally | Passed | Two `TestSideHTTP…` cases |
| Snapshot persistence before admission, follow-up after restart, no fabricated snapshots for old sessions | Passed | `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Reject continued use after cached profile deletion or model change | Passed | `TestSideRejectsDeletedOrChangedCachedProfile` |
| Cancel and await final answer/usage persistence before archive | Passed | `TestSideTaskDrainPersistsBeforeArchive` |
| Record usage once with side-question ownership when streaming consumer cancels early | Passed | `TestSideUsageRecordedOnceOnConsumerCancellation` |
| Automatically restored Worker/deadline contexts continue publishing snapshots | Passed | `TestSideRestoredWorkerRuntimePublishesNewCheckpoint` |
| Relevant package race checks | Passed | Commands below |
| TypeScript and production build | Passed | `npx tsc --noEmit`, `npm run build` |
| Biome for new frontend modules | Passed | `biome check`, three new modules |

Set `ARTEX_PG_DSN` for a separate disposable database to reproduce checks (never point it at production):

```sh
go test -race ./agent ./db ./server ./sidequestion ./llmrec ./llmpool \
  -run 'Test(Side|Checkpoint|Snapshot|BuildRequest|Service|MainSide|CaptureRun|TaskArchive|CompleteForwards|StopIntent|CancelIntent)' -count=1
cd web
npx tsc --noEmit
npx biome check src/lib/side-questions.ts src/hooks/use-side-questions.ts src/components/side-question-workspace.tsx
npm run build
```

The full Go regression suite was not entirely passing: two existing `server` tests failed during temporary-directory cleanup with `TempDir RemoveAll … directory not empty`:

- `TestInheritedActivityDetailAndRelationDeletion`
- `TestTaskMetadataPatchReturnsRenameAndPin`

Exporting the unmodified baseline and rerunning `server` in the same isolated environment reproduced both cleanup failures. The baseline also failed a goal-node count assertion in `TestCoreTaskLifecyclePG`; that assertion passed in the final modified `server` regression. Other packages, side-question cases, and race checks passed. Baseline failures were not counted as acceptance passes, and existing assertions were not changed to hide them.

Next.js emitted its existing multiple-lockfile/workspace-root inference warning; the build completed and generated all pages.

## Browser checks

Used Codex In-app Browser with independent local Go and Next.js development servers. Desktop and 390 × 844 layouts completed these automated user interactions, with screenshots and browser logs inspected:

- Entered `/btw` during ordinary chat execution; main and side content displayed concurrently, with the desktop sidebar working.
- Asked consecutive follow-ups; stopping retained partial side output while the main flow continued.
- Closing kept the request running; reopening restored the completed answer. After refresh, empty `/btw` restored history.
- Narrow-screen Drawer input, buttons, history, and closing worked without horizontal overflow.
- Clear used a confirmation dialog, removed history, and retained the main transcript and snapshot.
- Asked separately in task MainAgent and two Workers and switched between them; agent labels/history remained separate.
- A blocking local model fixture kept Worker running. Submitted `/btw` through its main input, then stopped the side question. Worker continued showing live execution and its own pause button; partial side output persisted.
- Browser error/warning logs were empty.

Controlled fixtures verified exact concurrency timing without relying on actual model speed. Two initial Worker checks lacked a valid concurrent window (task already ended / answer ended early); fixtures were corrected and checks repeated successfully. Those initial attempts were not counted as valid passes.

## Actual model conversations

First probed `grok-4.6` through the OpenAI-compatible endpoint `http://127.0.0.1:12580/tingly/openai`. HTTP 200 returned model `grok-4.6` and `READY` in 2.82 seconds. Since it was available, Tingly `glm` and Zhipu `glm-5.3` fallback services were not used or verified in this run.

| Scenario | Actual result |
| --- | --- |
| Ask about assets, goal, and marker while main session runs | Returned `redhaze.top`, homepage reading/summarization goal, and `BTW-REAL-0910`; completed in 16.97 seconds |
| Ask for tool evidence after main homepage reading | Correctly cited WebFetch 200, curl redirects 301 -> 302 -> 200, and page title; 7.24 seconds |
| Ask the side session to create a file with Bash | Execution refused; target file not created; 7.74 seconds |
| Completed side session leaves main context unchanged | Main transcript SHA-256 and activity records unchanged; zero side tool executions |
| Follow up after actually stopping/restarting Go service | Retained three prior exchanges and answered assets/marker/title from persisted snapshot without rerunning main agent |
| New session with Grok non-streaming profile | Correct assets and `ATOMIC-0910`; returned and persisted usage: input 11734, output 138, cache_read 11520 |

The asset case's main conversation used WebFetch and Bash/curl to read a public homepage, landing at `https://id.redhaze.top/home`, with the exact title “红幕科技 RedHaze Group · 全球综合集团门户”. Bash temporarily saved the response to a local test file; there was no remote write. This was verified separately from the side session's lack of tool execution.

Main transcript hash: `e7e61f135a4a120954b539f357e8c4205d7d5cd7460dcaf3dc0fd066463e1d00`.

**Usage limitation:** Tingly Grok streaming responses provided no usage. A direct `stream_options.include_usage=true` check returned HTTP 200 with 12 data frames and zero usage frames. Zero in these streaming tests means the endpoint supplied no usage, not that no billing occurred. Non-streaming usage and fixture failure/cancellation usage persisted correctly.

## Qwen review

Review model `qwen-flash`, OpenAI-compatible endpoint `https://dashscope.aliyuncs.com/compatible-mode/v1`, HTTP 200. Given the first three actual side conversations, main tool evidence, and engineering assertions, it returned `verdict: accept`, `concerns: []`, finding the answers consistent with the asset, marker, and page evidence, and tool refusal consistent with constraints. Review usage: prompt 6625, completion 312, total 6937.

This review did not include the later restart or non-streaming tests. Qwen's broad no-write characterization was inaccurate: main-session curl created a local response file, explicitly recorded above. Engineering assertions establish concurrency, zero tool execution, and transcript isolation; model review only assists answer-quality assessment.
