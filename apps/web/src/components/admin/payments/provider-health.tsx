import { Code, StatusBadge, When } from "@/components/admin/payments/common";
import { humanise } from "@/lib/payments/labels";
import type { ProviderContract, ProviderHealth } from "@/lib/payments/types";

/**
 * Stage 9 provider health: derived by the API from observations (calls, timeouts, circuit, declared outages,
 * verified webhooks, secret resolution). A provider with no recent observation is UNKNOWN — configuration alone is
 * never shown as healthy.
 */
export function HealthSummary({ health }: { health: ProviderHealth }) {
  const minutes = Math.round(health.window_seconds / 60);
  return (
    <div className="space-y-2">
      <p className="flex flex-wrap items-center gap-2">
        <StatusBadge status={health.status} />
        <span className="text-sm text-ink-700">{humanise(health.reason)}</span>
      </p>
      <p className="text-sm text-ink-700">
        Last {minutes} min: {health.calls} provider call{health.calls === 1 ? "" : "s"}, {health.errors} error{health.errors === 1 ? "" : "s"},{" "}
        {health.timeouts} timeout{health.timeouts === 1 ? "" : "s"}, {health.webhooks_verified} verified webhook{health.webhooks_verified === 1 ? "" : "s"}
        {health.p95_latency_ms !== null ? `, p95 ${health.p95_latency_ms} ms` : ""}.
      </p>
      <p className="text-sm text-ink-700">
        Last observation: <When at={health.last_observed_at} />
        {health.credentials_usable === false ? " · Credentials do not resolve from the secret manager." : ""}
        {health.outage_declared ? " · Outage declared by staff." : ""}
      </p>
    </div>
  );
}

const BASIS_TEXT: Record<string, string> = {
  OFFICIAL_PROVIDER_CONTRACT: "Official provider contract",
  PROVIDER_SANDBOX_OBSERVED: "Observed in the provider's sandbox",
  DOCUMENTED_BUT_UNVERIFIED: "Public documentation only — unverified",
  SIMULATED: "Simulated — FundZim's own simulator, no provider behind it",
};

export function ContractBasis({ contract }: { contract: ProviderContract | null }) {
  if (!contract) return <span>No adapter registered</span>;
  return (
    <span>
      {BASIS_TEXT[contract.basis] ?? humanise(contract.basis)}
      {contract.verified_on ? <span className="block text-sm text-ink-600">Verified on {contract.verified_on}</span> : null}
      {contract.sources.map((s) => (
        <span key={s} className="block text-sm">
          <Code>{s}</Code>
        </span>
      ))}
    </span>
  );
}

/** The execution environment of this deployment (one per process; never chosen by the browser). */
export function ExecutionEnvironmentNotice({ env }: { env: string }) {
  const text: Record<string, string> = {
    LOCAL_SIMULATOR: "Local simulator: payments are simulated by FundZim's sandbox; no provider is contacted and no money moves.",
    PROVIDER_SANDBOX: "Provider sandbox: test payments go to providers' sandbox accounts; no real money moves and none counts as a donation.",
    DISABLED: "Payment execution is disabled in this deployment.",
    PRODUCTION: "Production execution is configured, but live execution stays disabled in code until an ADR, legal sign-off and owner acceptance.",
  };
  return (
    <p className="rounded-card border border-line bg-surface p-4 text-ink-800">
      Execution environment: <Code>{env}</Code> — {text[env] ?? "Unknown environment."}
    </p>
  );
}
