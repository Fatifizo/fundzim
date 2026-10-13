import { describe, expect, it } from "vitest";

import {
  readAttemptDetail,
  readEvidence,
  readHealth,
  readHealthView,
  readProviderDetail,
  readAttempts,
  readInboxDetail,
  readIntentDetail,
  readOverview,
  readPaymentMoney,
  readProviders,
  readRecons,
  readRefund,
  readRejections,
  readSandboxState,
} from "./normalise";

const provider = { code: "sandbox", display_name: "FundZim Sandbox", simulated: true };

describe("payments normalisers", () => {
  it("money: digit strings + ISO code only; numbers are refused", () => {
    expect(readPaymentMoney("2550", "USD")).toEqual({ amount_minor: "2550", currency: "USD" });
    expect(readPaymentMoney(2550, "USD")).toBeNull();
    expect(readPaymentMoney("25.50", "USD")).toBeNull();
    expect(readPaymentMoney("-1", "USD")).toBeNull();
    expect(readPaymentMoney("0100", "USD")).toBeNull();
    expect(readPaymentMoney("100", "usd")).toBeNull();
    expect(readPaymentMoney("100", undefined)).toBeNull();
  });

  it("overview: counts only, from a map or a list; defaults are safe (live execution off)", () => {
    const o = readOverview({
      attempts_by_status: { UNKNOWN: 2, SUCCEEDED: 5, BAD: "x" },
      unknown_count: 2,
      needs_reconciliation_count: 1,
      inbox_by_status: [{ status: "DEAD_LETTERED", count: 1 }],
      rejections_24h: 3,
      open_refund_requests: 1,
      providers: [{ ...provider, status: "ENABLED", circuit: "OPEN" }, { nope: true }],
      live_execution: { enabled: false, constant: false, kill_switch: false },
      sandbox_enabled: true,
    });
    expect(o.attempts_by_status).toEqual([
      { status: "UNKNOWN", count: 2 },
      { status: "SUCCEEDED", count: 5 },
      { status: "BAD", count: 0 },
    ]);
    expect(o.inbox_by_status).toEqual([{ status: "DEAD_LETTERED", count: 1 }]);
    expect(o.providers).toEqual([{ ...provider, status: "ENABLED", circuit: "OPEN" }]);
    expect(o.sandbox_enabled).toBe(true);
    const empty = readOverview("garbage");
    expect(empty.live_execution).toEqual({ enabled: false, constant: false, kill_switch: false });
    expect(empty.sandbox_enabled).toBe(false);
    expect(empty.unknown_count).toBe(0);
    // "truthy" is not true.
    expect(readOverview({ sandbox_enabled: "true", live_execution: { enabled: 1 } }).live_execution.enabled).toBe(false);
  });

  it("attempt lists accept {items} or arrays and drop malformed rows", () => {
    const row = { id: "p1", donation_intent_id: "i1", campaign_id: "c1", attempt_number: 2, method: "ECOCASH", method_category: "MOBILE_MONEY", status: "UNKNOWN", amount_minor: "2550", currency: "USD", provider, simulated: true, needs_reconciliation: true, unknown_since: "2026-10-12T08:00:00Z", unknown_reason: "TIMEOUT", failure_code: null, created_at: "2026-10-12T07:59:00Z", updated_at: "2026-10-12T08:00:00Z" };
    const items = readAttempts({ items: [row, { id: 5 }, null], next_cursor: "x" });
    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({ id: "p1", amount: { amount_minor: "2550", currency: "USD" }, simulated: true, needs_reconciliation: true, unknown_reason: "TIMEOUT", provider });
    expect(readAttempts([row])).toHaveLength(1);
    expect(readAttempts(undefined)).toEqual([]);
    expect(readAttempts([{ ...row, amount_minor: 2550 }])[0]!.amount).toBeNull();
  });

  it("attempt detail keeps the raw provider status verbatim next to the mapped status", () => {
    const d = readAttemptDetail({
      id: "p1",
      status: "UNKNOWN",
      amount_minor: "2550",
      currency: "USD",
      provider,
      simulated: true,
      provider_calls: [{ id: "c1", attempt_no: 1, operation: "CREATE", classification: "OUTCOME_UNKNOWN", unknown_reason: "TIMEOUT", latency_ms: 30000, http_status: null }],
      transaction: { raw_status: "  Limbo ", mapped_status: "UNKNOWN", mapping_version: 3, reported_amount_minor: "2550", reported_currency: "USD" },
      references: [{ ref_kind: "PAYMENT", reference: "sbx_1", source: "POLL" }, { ref_kind: "PAYMENT" }],
      session: { id: "s1", kind: "HANDSET_APPROVAL", status: "OPEN", token: "secret-token" },
      webhooks: [{ id: "w1", provider, status: "DEAD_LETTERED", simulated: true }],
      events: [{ id: "e1", applied: false, not_applied_reason: "LOWER_RANK", source: "WEBHOOK" }, { id: "e2", applied: true }],
      reconciliation: [{ id: "r1", payment_id: "p1", status: "OPEN", expected_amount_minor: "2550", expected_currency: "USD" }],
      refund_requests: [{ id: "rf1", amount_minor: "100", currency: "USD", status: "APPROVED" }],
    })!;
    expect(d.provider_calls[0]).toMatchObject({ classification: "OUTCOME_UNKNOWN", unknown_reason: "TIMEOUT", latency_ms: 30000, http_status: null });
    expect(d.transaction).toMatchObject({ raw_status: "  Limbo ", mapped_status: "UNKNOWN", mapping_version: "3", reported_amount: { amount_minor: "2550", currency: "USD" } });
    expect(d.references).toHaveLength(1);
    expect(d.session).toEqual({ id: "s1", kind: "HANDSET_APPROVAL", status: "OPEN", redirect_url: null, expires_at: null, returned_at: null });
    expect(JSON.stringify(d)).not.toContain("secret-token");
    expect(d.events.map((e) => [e.applied, e.not_applied_reason])).toEqual([
      [false, "LOWER_RANK"],
      [true, null],
    ]);
    expect(d.reconciliation[0]!.expected).toEqual({ amount_minor: "2550", currency: "USD" });
    expect(d.refund_requests[0]!.execution).toBe("DISABLED");
    expect(readAttemptDetail({ nope: 1 })).toBeNull();
  });

  it("intent detail, inbox detail, rejections and recon references", () => {
    const i = readIntentDetail({ id: "i1", amount_minor: "500", currency: "USD", simulated: true, attempts: [{ id: "p1" }], events: [{ id: "e1" }] })!;
    expect(i).toMatchObject({ id: "i1", amount: { amount_minor: "500", currency: "USD" }, simulated: true });
    expect(i.attempts).toHaveLength(1);
    expect(i.events).toHaveLength(1);
    const w = readInboxDetail({ id: "w1", provider, status: "DEAD_LETTERED", payload_redacted: { a: 1 }, raw_body_sha256: "a".repeat(64), headers_subset: "nope" })!;
    expect(w.payload_redacted).toEqual({ a: 1 });
    expect(w.raw_body_sha256).toBe("a".repeat(64));
    expect(w.headers_subset).toBeNull();
    expect(readInboxDetail({ id: "w2", raw_body_sha256: "not-hex" })!.raw_body_sha256).toBeNull();
    expect(readRejections({ items: [{ id: "x", provider_code: "sandbox", reason_code: "SIGNATURE_INVALID", body_bytes: 10, body_sha256: null }] })).toEqual([
      { id: "x", provider_code: "sandbox", reason_code: "SIGNATURE_INVALID", body_bytes: 10, body_sha256: null, received_at: null },
    ]);
    const [r] = readRecons([{ id: "r1", status: "OPEN", expected_amount_minor: "1", expected_currency: "USD", reported_amount_minor: null, reported_currency: null }]);
    expect(r!.reported).toBeNull();
  });

  it("refund: requester as id or object; execution is DISABLED unless the API says otherwise", () => {
    expect(readRefund({ id: "r1", requested_by: "u1" })!.requested_by).toEqual({ id: "u1", display_name: null });
    expect(readRefund({ id: "r1", requested_by: { id: "u1", display_name: "Maker" } })!.requested_by).toEqual({ id: "u1", display_name: "Maker" });
    expect(readRefund({ id: "r1" })!.execution).toBe("DISABLED");
    expect(readRefund({ id: "r1", execution: "DISABLED" })!.execution).toBe("DISABLED");
    expect(readRefund(null)).toBeNull();
  });

  it("providers: secret reference names only; flags are strict booleans", () => {
    const v = readProviders({
      items: [
        {
          ...provider,
          status: "ENABLED",
          circuit: "CLOSED",
          accounts: [{ id: "a1", environment: "SANDBOX", webhook_secret_ref: "env:X", secrets_resolvable: true, unexpected_field: "dropped-by-normaliser" }],
          capabilities: [{ id: "c1", method: "ECOCASH", currency: "USD", routable: true, flags: { refund_api: "yes", status_api: true } }],
        },
      ],
      live_execution: { enabled: false, constant: false, kill_switch: false },
      sandbox_enabled: true,
    });
    expect(v.items[0]!.accounts[0]).toMatchObject({ webhook_secret_ref: "env:X", secrets_resolvable: true });
    expect(JSON.stringify(v)).not.toContain("dropped-by-normaliser");
    expect(v.items[0]!.capabilities[0]!.flags).toMatchObject({ refund_api: false, status_api: true });
    expect(v.sandbox_enabled).toBe(true);
  });

  it("sandbox state is always marked simulated", () => {
    expect(readSandboxState({ raw_status: "limbo", amount_minor: "2550", currency: "USD", simulated: false })).toMatchObject({ raw_status: "limbo", simulated: true, amount: { amount_minor: "2550", currency: "USD" } });
    expect(readSandboxState(null)).toBeNull();
  });
});

describe("Stage 9 normalisers", () => {
  it("health: unknown or missing status is UNKNOWN, never healthy; counts are non-negative", () => {
    expect(readHealth({ status: "HEALTHY", reason: "RECENT_CALLS_SUCCEEDED", calls: 3 }).status).toBe("HEALTHY");
    expect(readHealth({ status: "GREAT" }).status).toBe("UNKNOWN");
    expect(readHealth(undefined).status).toBe("UNKNOWN");
    expect(readHealth({ calls: -4, errors: "2" }).calls).toBe(0);
    expect(readHealth({ credentials_usable: "yes" }).credentials_usable).toBeNull();
  });

  it("providers: Stage 9 fields with safe defaults; an unknown contract basis is never shown as verified", () => {
    const v = readProviders({
      execution_environment: "LOCAL_SIMULATOR",
      allowlist: ["sandbox", 3],
      items: [{ ...provider, id: "p1", sandbox_status: "ENABLED", contract: { basis: "TRUST_ME", sources: ["x", 1] }, health: { status: "DEGRADED" } }],
    });
    expect(v.execution_environment).toBe("LOCAL_SIMULATOR");
    expect(v.allowlist).toEqual(["sandbox"]);
    expect(v.items[0]?.contract).toEqual({ basis: "DOCUMENTED_BUT_UNVERIFIED", sources: ["x"], verified_on: null });
    expect(v.items[0]?.health.status).toBe("DEGRADED");
    expect(v.items[0]?.usable).toBe(false);
    expect(readProviders({}).execution_environment).toBe("UNKNOWN");
  });

  it("provider detail: error categories keep only well-formed keys", () => {
    const d = readProviderDetail({ ...provider, id: "p1", error_categories_24h: { PROVIDER_TIMEOUT: 2, "bad key": 5, PAYMENT_DECLINED: -1 }, recent_health_events: [{ id: "e1", event_kind: "TIMEOUT" }, { nope: 1 }] });
    expect(d?.error_categories_24h).toEqual({ PROVIDER_TIMEOUT: 2, PAYMENT_DECLINED: 0 });
    expect(d?.recent_health_events).toHaveLength(1);
    expect(readProviderDetail({})).toBeNull();
  });

  it("health view and evidence: unknown consistency is shown as a contradiction; money stays a digit string", () => {
    const h = readHealthView({ execution_environment: "PROVIDER_SANDBOX", secret_manager: "UNAVAILABLE", items: [{ id: "p1", provider, usable: true, health: {} }] });
    expect(h.items[0]?.health.status).toBe("UNKNOWN");
    expect(h.secret_manager).toBe("UNAVAILABLE");
    const ev = readEvidence({ items: [{ id: "e1", raw_status: "paid", mapped_status: "SUCCEEDED", mapped: true, consistency: "WEIRD", reported_amount_minor: "1024", reported_currency: "USD" }, { id: "e2", reported_amount_minor: 10, reported_currency: "USD" }] });
    expect(ev[0]?.consistency).toBe("CONTRADICTS");
    expect(ev[0]?.reported).toEqual({ amount_minor: "1024", currency: "USD" });
    expect(ev[1]?.reported).toBeNull();
  });
});
