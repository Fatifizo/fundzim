/**
 * Defensive readers for campaign API responses. The contract fixes endpoints and field names but leaves
 * several response shapes open; these functions accept the documented reading (./types.ts) plus the obvious
 * variants (flat or nested objects, map or list), drop anything malformed, and never throw. Money stays a
 * digit string throughout.
 */
import type { Money } from "@/lib/api/types";

import { reasonsFromDetails } from "./eligibility";
import type {
  AgeAttestation,
  Campaign,
  CampaignMedia,
  CampaignReview,
  CampaignUpdate,
  Category,
  CurrencyInfo,
  EligibilityResult,
  ModerationItem,
  PersonRef,
  PublicCampaign,
  PublicCampaignSummary,
  ReviewHistoryEntry,
  StaffAction,
  StaffCampaignDetail,
  StaffCampaignSummary,
  StaffLinkStatus,
} from "./types";
import { STAFF_ACTIONS } from "./types";

type Rec = Record<string, unknown>;

const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown, fallback = ""): string => (typeof v === "string" ? v : fallback);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const bool = (v: unknown): boolean => v === true;
const int = (v: unknown, fallback = 0): number => (typeof v === "number" && Number.isInteger(v) ? v : fallback);
/** A list response: a bare array, or `{items | media | updates: [...]}` (the Go handlers use all three). */
const list = (v: unknown): unknown[] => {
  if (Array.isArray(v)) return v;
  if (!isRec(v)) return [];
  for (const key of ["items", "media", "updates"]) if (Array.isArray(v[key])) return v[key] as unknown[];
  return [];
};

export function readMoney(v: unknown): Money | null {
  if (!isRec(v)) return null;
  const amount = v.amount_minor;
  const currency = v.currency;
  if (typeof amount !== "string" || !/^\d{1,19}$/.test(amount) || typeof currency !== "string") return null;
  return { amount_minor: amount, currency: currency as Money["currency"] };
}

function person(v: unknown): PersonRef | null {
  if (typeof v === "string" && v !== "") return { id: v, display_name: "Staff member" };
  if (!isRec(v) || typeof v.id !== "string") return null;
  return { id: v.id, display_name: str(v.display_name, "Staff member") };
}

/** `next_cursor` from a `{items, next_cursor}` payload, else from the envelope meta. */
export function readNextCursor(data: unknown, meta?: { next_cursor?: string }): string | null {
  if (isRec(data) && typeof data.next_cursor === "string" && data.next_cursor !== "") return data.next_cursor;
  return meta?.next_cursor ?? null;
}

export function readEligibility(v: unknown): EligibilityResult[] {
  const one = (x: unknown, action?: string): EligibilityResult | null => {
    if (!isRec(x)) return null;
    const reasons = Array.isArray(x.reasons) ? reasonsFromDetails(x.reasons as Rec[]) : [];
    return { action: str(x.action, action ?? ""), allowed: bool(x.allowed), reasons };
  };
  if (Array.isArray(v)) return v.map((x) => one(x)).filter((x): x is EligibilityResult => x !== null);
  if (isRec(v)) {
    if ("allowed" in v) {
      const r = one(v);
      return r ? [r] : [];
    }
    return Object.entries(v)
      .map(([action, x]) => one(x, action))
      .filter((x): x is EligibilityResult => x !== null);
  }
  return [];
}

export function readCampaign(v: unknown): Campaign {
  const c = isRec(v) ? v : {};
  const ben = isRec(c.beneficiary) ? c.beneficiary : null;
  const fb = isRec(c.feedback) ? c.feedback : isRec(c.review_feedback) ? c.review_feedback : null;
  const category = isRec(c.category) ? str(c.category.code) : str(c.category ?? c.category_code);
  return {
    id: str(c.id),
    public_code: strOrNull(c.public_code),
    slug: strOrNull(c.slug),
    status: str(c.status, "DRAFT"),
    visibility: str(c.visibility, "PUBLIC"),
    category,
    organisation_id: strOrNull(c.organisation_id ?? (isRec(c.owner) && c.owner.type === "ORGANISATION" ? c.owner.id : null)),
    title: str(c.title),
    summary: str(c.summary),
    story: str(c.story),
    goal: readMoney(c.goal),
    risk_tier: strOrNull(c.risk_tier),
    re_review_required: bool(c.re_review_pending) || bool(c.re_review_required),
    resubmission_count: int(c.resubmission_count),
    beneficiary: ben
      ? {
          beneficiary_id: str(ben.beneficiary_id ?? ben.id),
          display_name: strOrNull(ben.display_name),
          disclosure: ben.disclosure === "DISPLAY_NAME" ? "DISPLAY_NAME" : "NONE",
          consent_declared: bool(ben.consent_declared),
          verification_status: strOrNull(ben.verification_status ?? (isRec(ben.verification) ? ben.verification.status : null)),
        }
      : null,
    review_feedback: fb ? { outcome: str(fb.outcome), reason_code: strOrNull(fb.reason_code), message: strOrNull(fb.message ?? fb.user_message), decided_at: strOrNull(fb.decided_at) } : null,
    eligibility: readEligibility(c.eligibility),
    completion_reason: strOrNull(c.completion_reason),
    created_at: strOrNull(c.created_at),
    submitted_at: strOrNull(c.submitted_at),
    approved_at: strOrNull(c.approved_at),
    published_at: strOrNull(c.published_at),
    paused_at: strOrNull(c.paused_at),
    completed_at: strOrNull(c.completed_at),
    cancelled_at: strOrNull(c.cancelled_at),
    archived_at: strOrNull(c.archived_at),
    updated_at: strOrNull(c.updated_at),
    version: int(c.version, 0),
  };
}

export function readCampaigns(v: unknown): Campaign[] {
  return list(v).map(readCampaign).filter((c) => c.id !== "");
}

export function readCategories(v: unknown): Category[] {
  return list(v)
    .filter(isRec)
    .filter((c) => typeof c.code === "string" && c.active !== false)
    .map((c) => ({ code: c.code as string, name: str(c.name, c.code as string), description: strOrNull(c.description), requires_organisation: bool(c.requires_organisation) }));
}

/** Only currencies the API marks usable: an explicit `available: false` (or `enabled: false`) hides it. */
export function readCurrencies(v: unknown): CurrencyInfo[] {
  return list(v)
    .filter(isRec)
    .filter((c) => typeof c.code === "string" && /^[A-Z]{3}$/.test(c.code) && c.available !== false && c.enabled !== false && c.minor_units_verified !== false)
    .filter((c) => typeof c.minor_units === "number" && Number.isInteger(c.minor_units) && c.minor_units >= 0 && c.minor_units <= 6)
    .map((c) => ({ code: c.code as string, minor_units: c.minor_units as number, display_symbol: str(c.display_symbol, c.code as string) }));
}

export function readMedia(v: unknown): CampaignMedia[] {
  return list(v)
    .filter(isRec)
    .filter((m) => typeof m.id === "string")
    .map((m) => ({
      id: m.id as string,
      kind: m.kind === "COVER" || m.kind === "COVER_IMAGE" ? "COVER" : "GALLERY",
      position: int(m.position),
      alt_text: str(m.alt_text),
      status: str(m.status, "UPLOADED"),
      rejection_reason: strOrNull(m.rejection_reason ?? m.rejected_reason),
      created_at: strOrNull(m.created_at),
    }))
    .sort((a, b) => (a.kind === b.kind ? a.position - b.position : a.kind === "COVER" ? -1 : 1));
}

export function readUpdates(v: unknown): CampaignUpdate[] {
  return list(v)
    .filter(isRec)
    .filter((u) => typeof u.id === "string")
    .map((u) => ({ id: u.id as string, title: str(u.title), body: str(u.body), status: str(u.status, "PUBLISHED"), created_at: strOrNull(u.created_at), published_at: strOrNull(u.published_at) }));
}

function readCategoryRef(v: unknown, fallbackCode?: unknown): { code: string; name: string } {
  if (isRec(v)) return { code: str(v.code), name: str(v.name, str(v.code)) };
  const code = str(v, str(fallbackCode));
  return { code, name: code ? code.charAt(0) + code.slice(1).toLowerCase().replace(/_/g, " ") : "" };
}

/** Cover and gallery ids: `cover_media_id` + `media_ids` (Go public view), or older `cover`/`media` objects. */
function readCover(c: Rec): { id: string; alt_text: string } | null {
  if (typeof c.cover_media_id === "string" && c.cover_media_id) return { id: c.cover_media_id, alt_text: str(c.cover_alt_text) };
  if (isRec(c.cover) && typeof c.cover.id === "string") return { id: c.cover.id, alt_text: str(c.cover.alt_text) };
  const media = list(c.media).filter(isRec);
  const cover = media.find((m) => (m.kind === "COVER" || m.kind === "COVER_IMAGE") && typeof m.id === "string");
  return cover ? { id: cover.id as string, alt_text: str(cover.alt_text) } : null;
}

function readGallery(c: Rec, coverId: string | null): Array<{ id: string; alt_text: string }> {
  if (Array.isArray(c.media_ids)) {
    return c.media_ids.filter((id): id is string => typeof id === "string" && id !== coverId).map((id) => ({ id, alt_text: "" }));
  }
  return list(c.media)
    .filter(isRec)
    .filter((m) => (m.kind === "GALLERY" || m.kind === "GALLERY_IMAGE") && typeof m.id === "string")
    .map((m) => ({ id: m.id as string, alt_text: str(m.alt_text) }));
}

/** Public beneficiary: `{disclosed, display_name?}` (Go) or `{disclosure, display_name}` (older reading). */
function readPublicBeneficiary(ben: Rec): PublicCampaign["beneficiary"] {
  const disclosed = ben.disclosed === true || ben.disclosure === "DISPLAY_NAME";
  const name = disclosed ? strOrNull(ben.display_name) : null;
  return { disclosure: disclosed && name ? "DISPLAY_NAME" : "NONE", display_name: name };
}

export function readPublicCampaign(v: unknown): PublicCampaign {
  const c = isRec(v) ? v : {};
  const organiser = isRec(c.organiser) ? c.organiser : {};
  const cover = readCover(c);
  return {
    slug: str(c.slug),
    title: str(c.title),
    summary: str(c.summary),
    story: str(c.story),
    category: readCategoryRef(c.category, c.category_code),
    goal: readMoney(c.goal),
    status: str(c.status),
    organiser: { display_name: str(organiser.display_name, "The organiser") },
    organisation:
      organiser.type === "ORGANISATION" && typeof organiser.display_name === "string"
        ? { display_name: organiser.display_name }
        : isRec(c.organisation) && typeof c.organisation.display_name === "string"
          ? { display_name: c.organisation.display_name }
          : null,
    beneficiary: readPublicBeneficiary(isRec(c.beneficiary) ? c.beneficiary : {}),
    cover,
    gallery: readGallery(c, cover?.id ?? null),
    published_at: strOrNull(c.published_at),
    completed_at: strOrNull(c.completed_at),
  };
}

export function readPublicSummaries(v: unknown): PublicCampaignSummary[] {
  return list(v)
    .filter(isRec)
    .filter((c) => typeof c.slug === "string")
    .map((c) => ({
      slug: c.slug as string,
      title: str(c.title),
      summary: str(c.summary),
      category: readCategoryRef(c.category, c.category_code),
      goal: readMoney(c.goal),
      status: str(c.status),
      cover: readCover(c),
      published_at: strOrNull(c.published_at),
    }));
}

/** Category display names from the categories list (codes from campaign responses are shown with their name). */
export function withCategoryName<T extends { category: { code: string; name: string } }>(item: T, categories: Category[]): T {
  const match = categories.find((c) => c.code === item.category.code);
  return match ? { ...item, category: { code: match.code, name: match.name } } : item;
}

export function readStaffSummaries(v: unknown): StaffCampaignSummary[] {
  return list(v)
    .filter(isRec)
    .map((x) => {
      const c = isRec(x.campaign) ? { ...x.campaign, ...x } : x;
      const owner = isRec(c.owner) ? c.owner : {};
      return {
        id: str(c.campaign_id ?? c.id),
        title: str(c.title),
        status: str(c.status),
        review_status: strOrNull(c.review_status),
        category: isRec(c.category) ? str(c.category.code) : str(c.category ?? c.category_code),
        risk_tier: strOrNull(c.risk_tier),
        owner_display_name: strOrNull(c.owner_display_name ?? owner.display_name),
        assigned_to: person(c.assigned_to),
        escalated: bool(c.escalated),
        awaiting_second_approval: bool(c.awaiting_second_approval),
        submitted_at: strOrNull(c.queued_at ?? c.submitted_at),
        updated_at: strOrNull(c.updated_at),
        slug: strOrNull(c.slug),
      };
    })
    .filter((c) => c.id !== "");
}

function readHistory(v: unknown): ReviewHistoryEntry[] {
  return list(v)
    .filter(isRec)
    .map((h, i) => ({
      id: str(h.id, `${String(h.version ?? "")}-${i}`),
      action: str(h.event_type ?? h.action, h.to_status ? `status: ${String(h.to_status)}` : "event"),
      from_status: strOrNull(h.from_status),
      to_status: strOrNull(h.to_status),
      actor: isRec(h.actor)
        ? { type: str(h.actor.type, "STAFF"), display_name: strOrNull(h.actor.display_name) }
        : strOrNull(h.actor_type)
          ? { type: str(h.actor_type), display_name: null }
          : null,
      reason_code: strOrNull(h.reason_code),
      note: strOrNull(h.note),
      occurred_at: strOrNull(h.occurred_at ?? h.created_at),
    }));
}

/**
 * Staff review detail (`GET /admin/campaigns/{id}/review`): `{campaign (owner view), risk_tier, owner,
 * beneficiary, restriction, media, review {…, pending_decided_by, requires_second_approval, …}, versions,
 * history, eligibility {APPROVE, PUBLISH}, policy}`. A first approval is pending while `pending_decided_by`
 * is set. `restriction` is reduced to a yes/no flag: the UI never shows a level or reason.
 */
export function readStaffDetail(v: unknown): StaffCampaignDetail {
  const d = isRec(v) ? v : {};
  const campaignRaw = isRec(d.campaign) ? d.campaign : d;
  const r = isRec(d.review) ? d.review : {};
  const ben = isRec(d.beneficiary) ? d.beneficiary : isRec(campaignRaw.beneficiary) ? campaignRaw.beneficiary : null;
  const owner = isRec(d.owner) ? d.owner : isRec(campaignRaw.owner) ? campaignRaw.owner : {};
  const pendingBy = strOrNull(isRec(r.pending_decided_by) ? r.pending_decided_by.id : r.pending_decided_by);
  const review: CampaignReview = {
    id: strOrNull(r.id),
    status: strOrNull(r.status),
    assigned_to: person(r.assigned_to ?? d.assigned_to),
    risk_tier: strOrNull(r.risk_tier ?? d.risk_tier ?? campaignRaw.risk_tier),
    pending_outcome: strOrNull(r.pending_outcome) ?? (pendingBy ? "APPROVE" : null),
    pending_decided_by: pendingBy,
    requires_second_approval: bool(r.requires_second_approval),
    escalated: bool(r.escalated),
    compliance_case_id: strOrNull(r.compliance_case_id),
  };
  const allowed = Array.isArray(d.allowed_actions)
    ? (d.allowed_actions.filter((a) => typeof a === "string" && (STAFF_ACTIONS as readonly string[]).includes(a)) as StaffAction[])
    : null;
  const restriction = d.restriction;
  const restricted =
    bool(d.restricted) ||
    (typeof restriction === "string" && restriction !== "" && restriction !== "NONE") ||
    (isRec(restriction) && (restriction.restricted === true || (typeof restriction.level === "string" && restriction.level !== "" && restriction.level !== "NONE")));
  return {
    campaign: { ...readCampaign(campaignRaw), owner_display_name: strOrNull(campaignRaw.owner_display_name ?? owner.display_name) },
    review,
    eligibility: readEligibility(d.eligibility ?? campaignRaw.eligibility),
    beneficiary: ben
      ? {
          display_name: strOrNull(ben.display_name),
          beneficiary_type: strOrNull(ben.beneficiary_type ?? ben.type),
          verification_status: strOrNull(ben.verification_status ?? (isRec(ben.verification) ? ben.verification.status : null)),
        }
      : null,
    restricted,
    media: readMedia(d.media),
    history: readHistory(d.history),
    allowed_actions: allowed,
  };
}

export function readModeration(v: unknown): ModerationItem[] {
  return list(v)
    .filter(isRec)
    .filter((u) => typeof u.id === "string")
    .map((u) => ({
      id: u.id as string,
      campaign_id: str(u.campaign_id),
      campaign_title: strOrNull(u.campaign_title),
      title: str(u.title),
      body: str(u.body),
      status: str(u.status),
      created_at: strOrNull(u.created_at),
    }));
}

/**
 * `GET`/`POST /me/age-attestation`: `{adult_age, statement_version, attestation {outcome, attested_at, source, …} | null,
 * assurance, level, basic_unmet?}`. `current` (older reading) is also accepted.
 */
export function readAgeAttestation(v: unknown): AgeAttestation {
  const d = isRec(v) ? v : {};
  const cur = isRec(d.attestation) ? d.attestation : isRec(d.current) ? d.current : {};
  const outcome = cur.outcome === "ATTESTED" || cur.outcome === "DECLINED" ? cur.outcome : null;
  return {
    outcome,
    statement_version: str(d.statement_version ?? cur.statement_version, "v1"),
    adult_age: int(d.adult_age ?? cur.adult_age, 18),
    attested_at: strOrNull(cur.attested_at ?? cur.created_at),
    source: strOrNull(cur.source),
    level: strOrNull(d.level),
    basic_unmet: Array.isArray(d.basic_unmet) ? d.basic_unmet.filter((x): x is string => typeof x === "string") : [],
  };
}

/** `GET /admin/me/personal-account-link`: `{linked, personal_email_masked, linked_at, pending {email_masked, expires_at} | null}`. */
export function readStaffLink(v: unknown): StaffLinkStatus {
  const d = isRec(v) ? v : {};
  const pending = isRec(d.pending) ? d.pending : null;
  if (typeof d.linked === "boolean") {
    return {
      status: d.linked ? "LINKED" : pending ? "PENDING" : "NONE",
      email_masked: strOrNull(d.linked ? d.personal_email_masked : pending?.email_masked),
      requested_at: null,
      linked_at: strOrNull(d.linked_at),
      expires_at: strOrNull(pending?.expires_at),
    };
  }
  return { status: str(d.status, "NONE"), email_masked: strOrNull(d.email_masked), requested_at: strOrNull(d.requested_at), linked_at: strOrNull(d.linked_at), expires_at: strOrNull(d.expires_at) };
}
