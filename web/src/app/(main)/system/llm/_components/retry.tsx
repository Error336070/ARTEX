"use client";

// Shared LLM retry controls: attempt count and interval for each of five layers.
//
// Layers from inner to outer: connection (SDK) → empty response (SDK) → same-provider safe window → failover circuit → intent rerun.
// The first three follow endpoints and allow profile overrides; the last two are process-wide global settings.
//
// All inputs use blank = unset semantics, matching backend db.RetryRule:
//   Attempts: blank/0 = built-in default | -1 = disable this layer | >0 = use this count
//   Interval: blank/0 = original exponential backoff | >0 = fixed interval in milliseconds

import * as React from "react";

import { Loader2Icon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { LLMRetryOverride, LLMRetryPolicy, LLMRetryRule } from "@/lib/types";

export const ZERO_RULE: LLMRetryRule = { attempts: 0, interval_ms: 0 };
export const ZERO_OVERRIDE: LLMRetryOverride = {
  connect: ZERO_RULE,
  empty: ZERO_RULE,
  stream: ZERO_RULE,
};
const ZERO_POLICY: LLMRetryPolicy = {
  ...ZERO_OVERRIDE,
  breaker: ZERO_RULE,
  intent: ZERO_RULE,
};

type LayerMeta = {
  title: string;
  /** Where this retry layer runs and who executes it */
  where: string;
  /** Errors handled by this layer, including status codes */
  trigger: string;
  /** Similar errors excluded from this layer */
  skips?: string;
  desc: string;
  attemptsLabel: string;
  /** Default attempt count for the placeholder */
  defAttempts: number;
  /** Default interval policy for the placeholder */
  defInterval: string;
  /** Meaning of an attempt count of -1 */
  offHint: string;
};

export const RETRY_LAYERS = {
  connect: {
    title: "Connection retries",
    where: "SDK · Before HTTP 200",
    trigger:
      "Connection failures before HTTP 200: resets, read/write timeouts, DNS failures, and HTTP 408, 429, 500, 502, 503, 504.",
    skips: "Other status codes (400 / 401 / 403 / 404 / 413 / 422, etc.) are deterministic rejections; retrying will fail again, so propagate immediately.",
    desc: "Resend the same request unchanged. Once streaming starts (HTTP 200 received), disconnections belong to another layer.",
    attemptsLabel: "Retry attempts",
    defAttempts: 3,
    defInterval: "0.5s→1s→2s exponential (8s cap)",
    offHint: "-1 = no retries; propagate failures immediately",
  },
  empty: {
    title: "Empty response retries",
    where: "SDK · openai format only",
    trigger:
      "HTTP 200 with finish_reason=stop but no content blocks: this can result from empty gateway frames, lost reasoning fields, or sampling failures.",
    skips: "Excludes empty content caused by max_tokens truncation; increase the output limit, since retrying would fail again.",
    desc: "Resends the full prompt, which is expensive for long contexts. Keep the count low.",
    attemptsLabel: "Retry attempts",
    defAttempts: 2,
    defInterval: "0.5s→1s→2s exponential (8s cap)",
    offHint: "-1 = return empty responses unchanged",
  },
  stream: {
    title: "Same-provider safe-window retries",
    where: "Application · Before output delivery",
    trigger:
      "Failures after streaming starts (HTTP 200): disconnection, provider overloaded, or in-stream 429 / 5xx errors, before any token reaches the caller.",
    skips:
      "No retries for exhausted quota (402 / insufficient_quota; failover switches profiles), long context (413 / context length; handled by compaction), or deterministic 400 / 401 / 403 / 404 / 422 rejections.",
    desc: "Replay the same request on the same profile. Since no output has been delivered, replay does not duplicate model output or tool execution.",
    attemptsLabel: "Retry attempts",
    defAttempts: 2,
    defInterval: "0.5s→1s exponential (4s cap)",
    offHint: "-1 = interrupted streams go directly to the outer intent rerun layer",
  },
  breaker: {
    title: "Failover circuit breaker",
    where: "Application · Process-wide global setting",
    trigger:
      "Transient failures (429, 5xx, network errors) open the circuit at the consecutive failure threshold. Deterministic failures such as insufficient balance (402), invalid keys (401 / 403), or missing models (404) open it immediately.",
    skips: "A success resets the count, so occasional failures do not accumulate toward opening the circuit.",
    desc: "An open circuit enters cooldown, during which failover skips the profile. State is stored and survives restarts.",
    attemptsLabel: "Consecutive failures before circuit opens",
    defAttempts: 3,
    defInterval: "1min→5min→30min steps",
    offHint: "-1 = transient failures never open the circuit (deterministic failures still do)",
  },
  intent: {
    title: "Intent reruns",
    where: "Application · Process-wide global setting",
    trigger:
      "The worker ends with model_error after inner retries are exhausted or a stream breaks after output delivery (replay is unsafe, so the full intent must restart).",
    skips: "Exhausted quota is handled by profile failover, not reruns here. Paused, terminated, or finalizing tasks yield immediately without waiting through backoff.",
    desc: "Restart the entire intent. This outermost layer multiplies the inner retry counts again for each rerun.",
    attemptsLabel: "Rerun attempts",
    defAttempts: 2,
    defInterval: "Fixed 3s",
    offHint: "-1 = no reruns; mark the intent blocked immediately",
  },
} satisfies Record<string, LayerMeta>;

type LayerKey = keyof typeof RETRY_LAYERS;

/** Readable milliseconds for display beside the input. */
function humanMs(ms: number) {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${Number((ms / 1000).toFixed(2))}s`;
  return `${Number((ms / 60_000).toFixed(2))}min`;
}

/** Controlled numeric input: blank ↔ 0; intermediate states ("-", "1e") stay local. */
function NumField({
  id,
  value,
  onChange,
  placeholder,
  min,
}: {
  id: string;
  value: number;
  onChange: (n: number) => void;
  placeholder: string;
  min: number;
}) {
  const [text, setText] = React.useState(value === 0 ? "" : String(value));
  // Sync when the parent replaces values (policy loaded or profile changed). Local typing does not reach this branch,
  // because value already equals the parsed local text.
  React.useEffect(() => {
    const incoming = value === 0 ? "" : String(value);
    setText((cur) => (Number(cur || 0) === value ? cur : incoming));
  }, [value]);
  return (
    <Input
      id={id}
      type="number"
      min={min}
      className="w-28 shrink-0"
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value);
        onChange(e.target.value.trim() === "" || !Number.isFinite(n) ? 0 : Math.trunc(n));
      }}
    />
  );
}

/** Two controls per retry layer. idPrefix keeps label htmlFor unique across multiple instances. */
export function RetryRuleFields({
  layer,
  idPrefix,
  value,
  onChange,
  compact,
}: {
  layer: LayerKey;
  idPrefix: string;
  value: LLMRetryRule;
  onChange: (r: LLMRetryRule) => void;
  /** true = compact profile drawer view, showing only the error trigger description */
  compact?: boolean;
}) {
  const meta = RETRY_LAYERS[layer];
  const human = humanMs(value.interval_ms);
  return (
    <div className={compact ? "grid gap-2" : "grid gap-3 rounded-lg border p-3"}>
      <div className="grid gap-0.5">
        <div className="flex flex-wrap items-baseline gap-2">
          <Label className="text-sm">{meta.title}</Label>
          <span className="text-muted-foreground text-xs">{meta.where}</span>
        </div>
        {/* Explain eligible errors and status codes; controls have no effect on errors outside this layer. */}
        <p className="text-muted-foreground text-xs">
          <span className="font-medium text-foreground">Trigger</span>: {meta.trigger}
        </p>
        {!compact && meta.skips && (
          <p className="text-muted-foreground text-xs">
            <span className="font-medium text-foreground">Excluded</span>: {meta.skips}
          </p>
        )}
        {!compact && <p className="text-muted-foreground text-xs">{meta.desc}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-n`} className="text-muted-foreground text-xs">
            {meta.attemptsLabel}
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-n`}
            min={-1}
            value={value.attempts}
            placeholder={`Default ${meta.defAttempts}`}
            onChange={(n) => onChange({ ...value, attempts: n })}
          />
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-ms`} className="text-muted-foreground text-xs">
            Interval (ms)
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-ms`}
            min={0}
            value={value.interval_ms}
            placeholder="Default backoff"
            onChange={(n) => onChange({ ...value, interval_ms: n })}
          />
          <span className="text-muted-foreground text-xs">{human ? `Fixed ${human}` : meta.defInterval}</span>
        </div>
      </div>
      {!compact && <p className="text-muted-foreground text-xs">Blank = default; {meta.offHint}.</p>}
    </div>
  );
}

/** Three endpoint-level overrides in the model profile drawer. */
export function ProfileRetryFields({
  value,
  onChange,
}: {
  value: LLMRetryOverride;
  onChange: (o: LLMRetryOverride) => void;
}) {
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-0.5">
        <Label className="text-sm">Retry overrides</Label>
        <p className="text-muted-foreground text-xs">
          Applies only to this profile, overriding global Retries and backoff settings. Blank = global setting; attempts -1 = disable this layer;
          an explicit interval replaces exponential backoff with a fixed interval. Circuit breaking and intent reruns are process-wide and can only be adjusted globally.
        </p>
      </div>
      {(["connect", "empty", "stream"] as const).map((k) => (
        <div key={k} className="border-t pt-3 first:border-t-0 first:pt-0">
          <RetryRuleFields
            compact
            layer={k}
            idPrefix="pf"
            value={value[k]}
            onChange={(r) => onChange({ ...value, [k]: r })}
          />
        </div>
      ))}
    </div>
  );
}

/** Retries and backoff tab: global defaults for all five layers. */
export function RetryPolicyPanel() {
  const [policy, setPolicy] = React.useState<LLMRetryPolicy>(ZERO_POLICY);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const p = await api.llmRetryPolicy();
      setPolicy({ ...ZERO_POLICY, ...p });
    } catch (e) {
      toast.error(`Failed to load retry policy: ${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    if (saving) return;
    setSaving(true);
    try {
      // The backend clamps out-of-range values; refresh from its response to display the stored values.
      const saved = await api.saveLLMRetryPolicy(policy);
      setPolicy({ ...ZERO_POLICY, ...saved });
      toast.success("Saved; applies immediately (the current call still uses previous parameters)");
    } catch (e) {
      toast.error(`Save failed: ${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  const set = (k: LayerKey) => (r: LLMRetryRule) => setPolicy((p) => ({ ...p, [k]: r }));

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-dashed p-10 text-muted-foreground text-sm">
        <Loader2Icon className="size-4 animate-spin" /> Loading retry policy…
      </div>
    );
  }

  return (
    <div className="grid gap-4">
      <div className="rounded-lg border bg-muted/30 p-3 text-muted-foreground text-xs leading-relaxed">
        A failed model call passes through five retry layers, from inner to outer:
        <span className="text-foreground"> Connection → Empty response → Same-provider safe window → Failover circuit → Intent rerun</span>
        . Outer layers run after inner attempts are exhausted, so attempt counts
        <span className="text-foreground">multiply</span>
        ; maximizing every layer can consume dozens of requests for one interruption.
        Leaving everything blank uses current defaults, preserving existing behavior. The first three layers can be overridden per model profile.
      </div>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {(Object.keys(RETRY_LAYERS) as LayerKey[]).map((k) => (
          <RetryRuleFields key={k} layer={k} idPrefix="gl" value={policy[k]} onChange={set(k)} />
        ))}
      </div>

      <div className="flex gap-2">
        <Button onClick={save} disabled={saving}>
          {saving ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}
          Save
        </Button>
        <Button variant="outline" onClick={() => setPolicy(ZERO_POLICY)} disabled={saving}>
          Restore all defaults
        </Button>
      </div>
    </div>
  );
}
