# English translation status

Date: 2026-10-11.

The operator interface, API errors, logs, notification copy, developer comments, configuration guidance, documentation, built-in agent prompts, interception judge prompt, tool descriptions, and synthetic demo content have been translated into English. The translation preserves protocol identifiers, routes, flags, formatting placeholders, attribution and the license. A Windows x64 binary with the final frontend is now built and smoke-tested.

## Remaining Chinese content

The remaining Chinese text is limited to exact legacy parser tokens and migration keys, historical transcripts and raw evidence, arbitrary Unicode test fixtures, Chinese document filenames, literal command examples, addresses, and ICP identifiers. The interceptor still requires its three original Chinese labels in the JSON comment field; translating those labels would break its parser. It accepts both `[Model]` and historical `[模型]` prefixes. Mention display names are English while original wire tokens remain compatible. The historical uploaded-file marker remains accepted.

Existing user-authored database records and customized or previously saved prompt bodies are not rewritten. Built-in records that already use an upsert receive the translated metadata through their existing initialization behavior. The previously shipped, unedited `traffic_search` description receives an exact-match English migration; other persisted first-insert-only defaults remain Chinese in existing databases and require separate operator review. Fresh databases receive the English defaults.

## Verification

- TypeScript typecheck passed after the final source changes.
- Four mention parsing and compatibility tests passed.
- The final production static UI build using webpack passed, and a Windows Go binary embeds its 397 exported files.
- The final binary passed browser checks on 21 navigation routes at desktop and mobile widths. Setup, terms acceptance, login, unauthenticated API rejection, and the real empty task API were checked. No visible Chinese text or document-level overflow appeared on those routes. The only browser console error was the expected 401 from an explicit unauthenticated API probe. No model or target workflow was launched.
- Browser review found spacing and a legacy rule display-label issue. Both were fixed in source. Their final typecheck and isolated text-rendering checks passed.
- The final root Go test run used a separate empty disposable PostgreSQL database per package: **904 passing tests/subtests, 12 skipped, 12 failures**. All 12 failing tests match the previously reproduced baseline failures; no prompt-translation test failed. The exact results are in `go-prompt-final.jsonl` and `go-prompt-final-summary.json` in the case directory.
- The nested `norma` module was tested separately: 12 packages passed and 4 failed. All 12 named failures in those four packages were reproduced in the original checkout.
- Source review confirmed that the previously reviewed administrator authentication protections remain structurally unchanged. The license is byte-for-byte unchanged.

The root Go failures are existing issues, not a passing release gate:

- Nine evidence/server tests reach an unchanged directory `Sync` operation that returns `Access is denied` on Windows. The original evidence test reproduces the same failure.
- One graph-overview test expects `coverage.scope`, which is absent in the original and translated implementation. The original test reproduces the failure.
- One usage-recording test omits the separate `EnsureLLMUsageTable` setup needed for its fixture. The original test reproduces the missing-table failure.
- One skill path test expects Unix separators on Windows. The original test reproduces the same separator mismatch.

## Handoff and build state

The final Windows x64 release package includes the embedded binary, skills, and a loopback launcher. PostgreSQL and an LLM profile must be configured before audits can run. The disposable database, setup password, and smoke-test runtime files were removed after verification. Existing database records that were customized or seeded under older versions retain their saved text; use a fresh database for a fully English default catalog.

The original verification artifacts and baseline comparisons are retained separately from this source export. No runtime settings, credentials, task history, or local evidence are included here.
