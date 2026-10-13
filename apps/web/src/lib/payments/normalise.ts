/**
 * Defensive readers for the Stage 8 staff payment API (docs/stage-8/interface-contracts.md §10), in the style of
 * lib/campaigns/normalise.ts: they accept the documented shape, drop anything malformed and never throw. Money
 * stays a digit string throughout (never Number/parseFloat); an amount that is not a valid digit string, or has no
 * valid currency code, becomes `null` and is shown as "—".
 */
import type {
  AdminAttempt,
  AdminIntent,
  AdminIntentDetail,
  AttemptDetail,
  CapabilityFlags,
  InboxDetail,
  InboxItem,
  LiveExecution,
  OverviewProvider,
  PaymentEvent,
  PaymentMoney,
  PaymentSession,
  PaymentsOverview,
  PersonRef,
  ProviderAccount,
  ProviderCall,
  ProviderCapability,
  ProviderRef,
  ProviderReference,
  ProviderRow,
  ProvidersView,
  ProviderTransaction,
  ReconReference,
  RefundRequest,
  SandboxState,
  WebhookRejection,
} from "./types";

type Rec = Record<string, unknown>;

const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const rec = (v: unknown): Rec => (isRec(v) ? v : {});
const str = (v: unknown, fallback = ""): string => (typeof v === "string" ? v : fallback);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const bool = (v: unknown): boolean => v === true;
const int = (v: unknown, fallback = 0): number => (typeof v === "number" && Number.isInteger(v) ? v : fallback);
const intOrNull = (v: unknown): number | null => (typeof v === "number" && Number.isInteger(v) ? v : null);
/** A list: a bare array or `{items: [...]}`. */
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : isRec(v) && Array.isArray(v.items) ? v.items : []);
const many = <T>(v: unknown, read: (x: unknown) => T | null): T[] => list(v).map(read).filter((x): x is T => x !== null);

const AMOUNT = /^(0|[1-9]\d{0,18})$/;
const CURRENCY = /^[A-Z]{3}$/;

/** `{amount_minor, currency}` from two flat fields; null unless both are well formed. */
export function readPaymentMoney(amount: unknown, currency: unknown): PaymentMoney | null {
  if (typeof amount !== "string" || !AMOUNT.test(amount) || typeof currency !== "string" || !CURRENCY.test(currency)) return null;
  return { amount_minor: amount, currency };
}

export function readProviderRef(v: unknown): ProviderRef {
  if (typeof v === "string") return { code: v, display_name: v, simulated: false };
  const p = rec(v);
  const code = str(p.code);
  return { code, display_name: str(p.display_name, code || "Unknown provider"), simulated: bool(p.simulated) };
}

function person(v: unknown): PersonRef | null {
  if (typeof v === "string" && v !== "") return { id: v, display_name: null };
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, display_name: strOrNull(v.display_name) };
}

/** `{STATUS: n}` (or `[{status, count}]`) → sorted pairs with non-negative integer counts. */
function counts(v: unknown): Array<{ status: string; count: number }> {
  const pairs: Array<{ status: string; count: number }> = [];
  if (Array.isArray(v)) {
    for (const x of v) if (isRec(x) && typeof x.status === "string") pairs.push({ status: x.status, count: Math.max(0, int(x.count)) });
  } else if (isRec(v)) {
    for (const [status, n] of Object.entries(v)) pairs.push({ status, count: Math.max(0, int(n)) });
  }
  return pairs;
}

export function readLiveExecution(v: unknown): LiveExecution {
  const l = rec(v);
  return { enabled: bool(l.enabled), constant: bool(l.constant), kill_switch: bool(l.kill_switch) };
}

export function readOverview(v: unknown): PaymentsOverview {
  const o = rec(v);
  const providers = (Array.isArray(o.providers) ? o.providers : []).flatMap((x): OverviewProvider[] => {
    if (!isRec(x) || typeof x.code !== "string") return [];
    return [{ ...readProviderRef(x), status: str(x.status, "UNKNOWN"), circuit: str(x.circuit, "UNKNOWN") }];
  });
  return {
    attempts_by_status: counts(o.attempts_by_status),
    unknown_count: Math.max(0, int(o.unknown_count)),
    needs_reconciliation_count: Math.max(0, int(o.needs_reconciliation_count)),
    inbox_by_status: counts(o.inbox_by_status),
    rejections_24h: Math.max(0, int(o.rejections_24h)),
    open_refund_requests: Math.max(0, int(o.open_refund_requests)),
    providers,
    live_execution: readLiveExecution(o.live_execution),
    sandbox_enabled: bool(o.sandbox_enabled),
  };
}

export function readEvent(v: unknown): PaymentEvent | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    subject_type: str(v.subject_type),
    from_status: strOrNull(v.from_status),
    to_status: strOrNull(v.to_status),
    applied: bool(v.applied),
    not_applied_reason: strOrNull(v.not_applied_reason),
    transition_code: strOrNull(v.transition_code),
    source: str(v.source),
    provider_event_id: strOrNull(v.provider_event_id),
    raw_status: strOrNull(v.raw_status),
    actor_type: str(v.actor_type),
    reason: strOrNull(v.reason),
    correlation_id: strOrNull(v.correlation_id),
    occurred_at: strOrNull(v.occurred_at),
    recorded_at: strOrNull(v.recorded_at),
  };
}

export function readIntent(v: unknown): AdminIntent | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    campaign_id: str(v.campaign_id),
    donor_user_id: strOrNull(v.donor_user_id),
    amount: readPaymentMoney(v.amount_minor, v.currency),
    status: str(v.status, "UNKNOWN"),
    is_anonymous: bool(v.is_anonymous),
    simulated: bool(v.simulated),
    expires_at: strOrNull(v.expires_at),
    created_at: strOrNull(v.created_at),
    updated_at: strOrNull(v.updated_at),
  };
}

export const readIntents = (v: unknown): AdminIntent[] => many(v, readIntent);

export function readIntentDetail(v: unknown): AdminIntentDetail | null {
  const base = readIntent(v);
  if (!base) return null;
  const d = rec(v);
  return { ...base, attempts: readAttempts(d.attempts), events: many(d.events, readEvent) };
}

export function readAttempt(v: unknown): AdminAttempt | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    donation_intent_id: str(v.donation_intent_id),
    campaign_id: str(v.campaign_id),
    attempt_number: int(v.attempt_number, 1),
    method: str(v.method),
    method_category: str(v.method_category),
    status: str(v.status, "UNKNOWN"),
    amount: readPaymentMoney(v.amount_minor, v.currency),
    provider: readProviderRef(v.provider),
    simulated: bool(v.simulated),
    needs_reconciliation: bool(v.needs_reconciliation),
    unknown_since: strOrNull(v.unknown_since),
    unknown_reason: strOrNull(v.unknown_reason),
    failure_code: strOrNull(v.failure_code),
    created_at: strOrNull(v.created_at),
    updated_at: strOrNull(v.updated_at),
  };
}

export const readAttempts = (v: unknown): AdminAttempt[] => many(v, readAttempt);

function readCall(v: unknown): ProviderCall | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    attempt_no: int(v.attempt_no, 1),
    operation: str(v.operation),
    classification: str(v.classification, "UNKNOWN"),
    error_class: strOrNull(v.error_class),
    unknown_reason: strOrNull(v.unknown_reason),
    http_status: intOrNull(v.http_status),
    provider_status_raw: strOrNull(v.provider_status_raw),
    provider_reference_returned: strOrNull(v.provider_reference_returned),
    latency_ms: intOrNull(v.latency_ms),
    correlation_id: strOrNull(v.correlation_id),
    started_at: strOrNull(v.started_at),
    deadline_at: strOrNull(v.deadline_at),
    completed_at: strOrNull(v.completed_at),
  };
}

function readTransaction(v: unknown): ProviderTransaction | null {
  if (!isRec(v)) return null;
  return {
    provider_transaction_ref: strOrNull(v.provider_transaction_ref),
    raw_status: typeof v.raw_status === "string" ? v.raw_status : null,
    raw_status_at: strOrNull(v.raw_status_at),
    mapped_status: strOrNull(v.mapped_status),
    mapping_version: typeof v.mapping_version === "number" ? String(v.mapping_version) : strOrNull(v.mapping_version),
    last_source: strOrNull(v.last_source),
    reported_amount: readPaymentMoney(v.reported_amount_minor, v.reported_currency),
    payer_instrument_masked: strOrNull(v.payer_instrument_masked),
    updated_at: strOrNull(v.updated_at),
  };
}

function readReference(v: unknown): ProviderReference | null {
  if (!isRec(v) || typeof v.reference !== "string") return null;
  return { ref_kind: str(v.ref_kind), reference: v.reference, source: str(v.source), recorded_at: strOrNull(v.recorded_at) };
}

function readSession(v: unknown): PaymentSession | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    kind: str(v.kind),
    status: str(v.status),
    redirect_url: strOrNull(v.redirect_url),
    expires_at: strOrNull(v.expires_at),
    returned_at: strOrNull(v.returned_at),
  };
}

export function readInboxItem(v: unknown): InboxItem | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    provider: readProviderRef(v.provider),
    provider_event_id: str(v.provider_event_id),
    event_class: str(v.event_class),
    event_type_raw: str(v.event_type_raw),
    our_reference: strOrNull(v.our_reference),
    provider_reference: strOrNull(v.provider_reference),
    status: str(v.status, "UNKNOWN"),
    attempts: Math.max(0, int(v.attempts)),
    last_error_code: strOrNull(v.last_error_code),
    simulated: bool(v.simulated),
    signature_key_slot: strOrNull(v.signature_key_slot),
    verification_method: strOrNull(v.verification_method),
    received_at: strOrNull(v.received_at),
    processed_at: strOrNull(v.processed_at),
  };
}

export const readInboxItems = (v: unknown): InboxItem[] => many(v, readInboxItem);

export function readInboxDetail(v: unknown): InboxDetail | null {
  const base = readInboxItem(v);
  if (!base) return null;
  const d = rec(v);
  const sha = typeof d.raw_body_sha256 === "string" && /^[0-9a-f]{64}$/i.test(d.raw_body_sha256) ? d.raw_body_sha256 : null;
  return {
    ...base,
    payload_redacted: isRec(d.payload_redacted) ? d.payload_redacted : null,
    raw_body_sha256: sha,
    dispatched_to: strOrNull(d.dispatched_to),
    parked_subject_id: strOrNull(d.parked_subject_id),
    parked_until_status: strOrNull(d.parked_until_status),
    park_deadline_at: strOrNull(d.park_deadline_at),
    next_attempt_at: strOrNull(d.next_attempt_at),
    headers_subset: isRec(d.headers_subset) ? d.headers_subset : null,
  };
}

export function readRejection(v: unknown): WebhookRejection | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    provider_code: str(v.provider_code),
    reason_code: str(v.reason_code),
    body_bytes: intOrNull(v.body_bytes),
    body_sha256: typeof v.body_sha256 === "string" && /^[0-9a-f]{64}$/i.test(v.body_sha256) ? v.body_sha256 : null,
    received_at: strOrNull(v.received_at),
  };
}

export const readRejections = (v: unknown): WebhookRejection[] => many(v, readRejection);

export function readRecon(v: unknown): ReconReference | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    payment_id: str(v.payment_id),
    reason: str(v.reason),
    status: str(v.status, "UNKNOWN"),
    expected: readPaymentMoney(v.expected_amount_minor, v.expected_currency),
    reported: readPaymentMoney(v.reported_amount_minor, v.reported_currency),
    provider_status_raw: typeof v.provider_status_raw === "string" ? v.provider_status_raw : null,
    provider_reference: strOrNull(v.provider_reference),
    evidence_at: strOrNull(v.evidence_at),
    created_at: strOrNull(v.created_at),
    resolved_at: strOrNull(v.resolved_at),
    resolution_note: strOrNull(v.resolution_note),
  };
}

export const readRecons = (v: unknown): ReconReference[] => many(v, readRecon);

export function readRefund(v: unknown): RefundRequest | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    payment_id: str(v.payment_id),
    amount: readPaymentMoney(v.amount_minor, v.currency),
    reason_code: str(v.reason_code),
    reason_text: str(v.reason_text),
    status: str(v.status, "UNKNOWN"),
    requested_by: person(v.requested_by),
    requested_at: strOrNull(v.requested_at),
    decided_by: person(v.decided_by),
    decided_at: strOrNull(v.decided_at),
    decision_reason: strOrNull(v.decision_reason),
    expires_at: strOrNull(v.expires_at),
    // Stage 8 never executes a refund (ADR-039 §4): anything other than an explicit value is shown as DISABLED.
    execution: strOrNull(v.execution) ?? "DISABLED",
  };
}

export const readRefunds = (v: unknown): RefundRequest[] => many(v, readRefund);

export function readAttemptDetail(v: unknown): AttemptDetail | null {
  const base = readAttempt(v);
  if (!base) return null;
  const d = rec(v);
  return {
    ...base,
    provider_payment_ref: strOrNull(d.provider_payment_ref),
    last_authoritative_source: strOrNull(d.last_authoritative_source),
    poll_attempts: Math.max(0, int(d.poll_attempts)),
    next_poll_at: strOrNull(d.next_poll_at),
    poll_horizon_at: strOrNull(d.poll_horizon_at),
    provider_calls: many(d.provider_calls, readCall),
    transaction: readTransaction(d.transaction),
    references: many(d.references, readReference),
    session: readSession(d.session),
    webhooks: readInboxItems(d.webhooks),
    events: many(d.events, readEvent),
    reconciliation: readRecons(d.reconciliation),
    refund_requests: readRefunds(d.refund_requests),
  };
}

const FLAG_KEYS: Array<keyof CapabilityFlags> = ["refund_api", "partial_refund", "idempotent_create", "supports_cancel", "signed_webhooks", "status_api", "separate_capture", "documented_expiry"];

function readCapability(v: unknown): ProviderCapability | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  const f = rec(v.flags);
  return {
    id: v.id,
    method: str(v.method),
    method_category: str(v.method_category),
    currency: str(v.currency),
    custody_model: str(v.custody_model),
    evidence_status: str(v.evidence_status),
    evidence_source: strOrNull(v.evidence_source),
    pcr_ref: strOrNull(v.pcr_ref),
    routable: bool(v.routable),
    capability_version: intOrNull(v.capability_version),
    flags: Object.fromEntries(FLAG_KEYS.map((k) => [k, bool(f[k])])) as unknown as CapabilityFlags,
  };
}

function readAccount(v: unknown): ProviderAccount | null {
  if (!isRec(v) || typeof v.id !== "string") return null;
  return {
    id: v.id,
    environment: str(v.environment),
    label: str(v.label),
    status: str(v.status),
    api_credentials_secret_ref: strOrNull(v.api_credentials_secret_ref),
    webhook_secret_ref: strOrNull(v.webhook_secret_ref),
    webhook_secret_ref_previous: strOrNull(v.webhook_secret_ref_previous),
    secrets_resolvable: bool(v.secrets_resolvable),
    webhook_replay_window_seconds: intOrNull(v.webhook_replay_window_seconds),
  };
}

export function readProviders(v: unknown): ProvidersView {
  const d = rec(v);
  const items = list(d).flatMap((x): ProviderRow[] => {
    if (!isRec(x) || typeof x.code !== "string") return [];
    return [
      {
        ...readProviderRef(x),
        status: str(x.status),
        selection_status: str(x.selection_status),
        confirmation_mode: str(x.confirmation_mode),
        circuit: str(x.circuit, "UNKNOWN"),
        accounts: many(x.accounts, readAccount),
        capabilities: many(x.capabilities, readCapability),
      },
    ];
  });
  return { items, live_execution: readLiveExecution(d.live_execution), sandbox_enabled: bool(d.sandbox_enabled) };
}

export function readSandboxState(v: unknown): SandboxState | null {
  if (!isRec(v)) return null;
  return {
    provider_payment_ref: strOrNull(v.provider_payment_ref),
    our_reference: strOrNull(v.our_reference),
    scenario: strOrNull(v.scenario),
    raw_status: typeof v.raw_status === "string" ? v.raw_status : null,
    amount: readPaymentMoney(v.amount_minor, v.currency),
    method: strOrNull(v.method),
    created_at: strOrNull(v.created_at),
    updated_at: strOrNull(v.updated_at),
    // Simulator state is simulated by definition, whatever the payload says.
    simulated: true,
  };
}
