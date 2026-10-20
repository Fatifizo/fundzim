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
  CampaignLocationOption,
  CategoryRef,
  CurrencyInfo,
  ListTotal,
  EligibilityResult,
  ModerationItem,
  PersonRef,
  PublicCampaign,
  PublicLocation,
  PublicCampaignSummary,
  PublicCategory,
  PublicImageRef,
  PublicOrganiser,
  PublicTrust,
  PublicUpdate,
  ReviewHistoryEntry,
  SitemapData,
  StaffAction,
  StaffReport,
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
    location_code: strOrNull(c.location_code),
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
      version: int(m.version, 0),
    }))
    .sort((a, b) => (a.kind === b.kind ? a.position - b.position : a.kind === "COVER" ? -1 : 1));
}

export function readUpdates(v: unknown): CampaignUpdate[] {
  return list(v)
    .filter(isRec)
    .filter((u) => typeof u.id === "string")
    .map((u) => ({ id: u.id as string, title: str(u.title), body: str(u.body), status: str(u.status, "PUBLISHED"), created_at: strOrNull(u.created_at), published_at: strOrNull(u.published_at), version: int(u.version, 0) }));
}

function humaniseCode(code: string): string {
  return code ? code.charAt(0) + code.slice(1).toLowerCase().replace(/_/g, " ") : "";
}

function readCategoryRef(v: unknown, fallbackCode?: unknown, fallbackName?: unknown): CategoryRef {
  if (isRec(v)) return { code: str(v.code), name: str(v.name, str(fallbackName, humaniseCode(str(v.code)))), slug: strOrNull(v.slug) };
  const code = str(v, str(fallbackCode));
  return { code, name: str(fallbackName, humaniseCode(code)), slug: null };
}

/** `variants: [{width, height}]` → ascending positive integer widths; null when the field is absent. */
export function readVariantWidths(v: unknown): number[] | null {
  if (!Array.isArray(v)) return null;
  const widths = v.filter(isRec).map((x) => x.width).filter((w): w is number => typeof w === "number" && Number.isInteger(w) && w > 0);
  return [...new Set(widths)].sort((a, b) => a - b);
}

/** Public media list `media: [{id, kind, alt_text, variants}]` (Go public view), keeping order. */
function publicMediaList(c: Rec): Array<PublicImageRef & { kind: string }> {
  return list(c.media)
    .filter(isRec)
    .filter((m) => typeof m.id === "string")
    .map((m) => ({ id: m.id as string, kind: str(m.kind), alt_text: str(m.alt_text), widths: readVariantWidths(m.variants) }));
}

/** Cover: `cover_media_id` (+ alt text and variants from `media`), or older `cover`/`media` objects. */
function readCover(c: Rec): PublicImageRef | null {
  const media = publicMediaList(c);
  if (typeof c.cover_media_id === "string" && c.cover_media_id) {
    const id = c.cover_media_id;
    const m = media.find((x) => x.id === id);
    return { id, alt_text: str(c.cover_alt_text, m?.alt_text ?? ""), widths: m ? m.widths : null };
  }
  if (isRec(c.cover) && typeof c.cover.id === "string") return { id: c.cover.id, alt_text: str(c.cover.alt_text), widths: readVariantWidths(c.cover.variants) };
  const cover = media.find((m) => m.kind === "COVER" || m.kind === "COVER_IMAGE");
  return cover ? { id: cover.id, alt_text: cover.alt_text, widths: cover.widths } : null;
}

function readGallery(c: Rec, coverId: string | null): PublicImageRef[] {
  const media = publicMediaList(c);
  if (Array.isArray(c.media_ids)) {
    return c.media_ids
      .filter((id): id is string => typeof id === "string" && id !== coverId)
      .map((id) => {
        const m = media.find((x) => x.id === id);
        return { id, alt_text: m?.alt_text ?? "", widths: m ? m.widths : null };
      });
  }
  return media.filter((m) => m.kind === "GALLERY" || m.kind === "GALLERY_IMAGE").map((m) => ({ id: m.id, alt_text: m.alt_text, widths: m.widths }));
}

/** Public beneficiary: `{disclosed, display_name?}` (Go) or `{disclosure, display_name}` (older reading). */
function readPublicBeneficiary(ben: Rec): PublicCampaign["beneficiary"] {
  const disclosed = ben.disclosed === true || ben.disclosure === "DISPLAY_NAME";
  const name = disclosed ? strOrNull(ben.display_name) : null;
  return { disclosure: disclosed && name ? "DISPLAY_NAME" : "NONE", display_name: name };
}

/** Trust indicators: only an explicit `true` counts (ADR-038 §5: shown only when true now). */
export function readTrust(v: unknown): PublicTrust {
  const t = isRec(v) ? v : {};
  return {
    organiser_identity_verified: t.organiser_identity_verified === true,
    organisation_verified: t.organisation_verified === true,
    beneficiary_verified: t.beneficiary_verified === true,
    campaign_reviewed: t.campaign_reviewed === true,
  };
}

function readOrganiser(v: unknown): PublicOrganiser | null {
  if (!isRec(v) || typeof v.display_name !== "string" || v.display_name.trim() === "") return null;
  return { type: str(v.type, "INDIVIDUAL"), display_name: v.display_name };
}

/** Public location `{code, name, kind, province_name, province_slug, town_slug}` or null (unknown shapes are dropped). */
export function readPublicLocation(v: unknown): PublicLocation | null {
  if (!isRec(v) || typeof v.name !== "string" || typeof v.province_slug !== "string" || !/^[a-z0-9-]{1,40}$/.test(v.province_slug)) return null;
  const town = strOrNull(v.town_slug);
  return { code: str(v.code), name: v.name, kind: str(v.kind), province_name: str(v.province_name, v.name), province_slug: v.province_slug,
    town_slug: town && /^[a-z0-9-]{1,40}$/.test(town) ? town : null };
}

export function readPublicCampaign(v: unknown): PublicCampaign {
  const c = isRec(v) ? v : {};
  const organiser = readOrganiser(c.organiser);
  const cover = readCover(c);
  const slug = str(c.slug);
  return {
    slug,
    canonical_slug: str(c.canonical_slug, slug),
    public_code: strOrNull(c.public_code),
    title: str(c.title),
    summary: str(c.summary),
    story: str(c.story),
    category: readCategoryRef(c.category, c.category_code, c.category_name),
    goal: readMoney(c.goal),
    status: str(c.status),
    organiser: organiser ?? { type: "INDIVIDUAL", display_name: "The organiser" },
    organisation:
      organiser?.type === "ORGANISATION"
        ? { display_name: organiser.display_name }
        : isRec(c.organisation) && typeof c.organisation.display_name === "string"
          ? { display_name: c.organisation.display_name }
          : null,
    beneficiary: readPublicBeneficiary(isRec(c.beneficiary) ? c.beneficiary : {}),
    cover,
    gallery: readGallery(c, cover?.id ?? null),
    trust: readTrust(c.trust),
    published_at: strOrNull(c.published_at),
    completed_at: strOrNull(c.completed_at),
    location: readPublicLocation(c.location),
  };
}

/** One public card (search, featured, category and discovery lists). */
export function readPublicSummary(c: Record<string, unknown>): PublicCampaignSummary {
  return {
    slug: c.slug as string,
    title: str(c.title),
    summary: str(c.summary),
    category: readCategoryRef(c.category, c.category_code, c.category_name),
    goal: readMoney(c.goal),
    status: str(c.status),
    organiser: readOrganiser(c.organiser),
    trust: readTrust(c.trust),
    cover: readCover(c),
    published_at: strOrNull(c.published_at),
    selection: c.selection === "FEATURED" || c.selection === "RECENTLY_PUBLISHED" ? c.selection : null,
  };
}

export function readPublicSummaries(v: unknown): PublicCampaignSummary[] {
  return list(v)
    .filter(isRec)
    .filter((c) => typeof c.slug === "string" && c.slug !== "")
    .map(readPublicSummary);
}

/** `total: {value, capped}` from the list payload (or meta); a bare number is accepted as exact. */
export function readTotal(data: unknown, meta?: unknown): ListTotal | null {
  for (const source of [data, meta]) {
    if (!isRec(source)) continue;
    const t = source.total;
    if (typeof t === "number" && Number.isInteger(t) && t >= 0) return { value: t, capped: false };
    if (isRec(t) && typeof t.value === "number" && Number.isInteger(t.value) && t.value >= 0) return { value: t.value, capped: t.capped === true };
  }
  return null;
}

const CATEGORY_SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

function readPublicCategoryRec(c: Rec): PublicCategory | null {
  if (typeof c.code !== "string" || c.code === "" || c.active === false) return null;
  const slug = typeof c.slug === "string" && CATEGORY_SLUG.test(c.slug) ? c.slug : null;
  if (!slug) return null;
  const live = c.live_campaigns;
  return {
    code: c.code,
    slug,
    name: str(c.name, humaniseCode(c.code)),
    description: strOrNull(c.description),
    display_order: int(c.display_order, 0),
    sensitive: c.sensitive === true,
    live_campaigns: typeof live === "number" && Number.isInteger(live) && live >= 0 ? live : null,
  };
}

/** `GET /public/categories` → `{items: [...]}`, ordered by `display_order` then name. */
export function readPublicCategories(v: unknown): PublicCategory[] {
  return list(v)
    .filter(isRec)
    .map(readPublicCategoryRec)
    .filter((c): c is PublicCategory => c !== null)
    .sort((a, b) => a.display_order - b.display_order || a.name.localeCompare(b.name));
}

export function readPublicCategory(v: unknown): PublicCategory | null {
  return isRec(v) ? readPublicCategoryRec(v) : null;
}

/** Category display name and slug from the categories list (codes from campaign responses). */
export function withCategoryName<T extends { category: CategoryRef }>(item: T, categories: ReadonlyArray<{ code: string; name: string; slug?: string | null }>): T {
  const match = categories.find((c) => c.code === item.category.code || (c.slug && c.slug === item.category.code));
  return match ? { ...item, category: { code: match.code, name: match.name, slug: match.slug ?? item.category.slug } } : item;
}

/** Published updates for the public page (`{items, next_cursor}`), with their approved media ids. */
export function readPublicUpdates(v: unknown): PublicUpdate[] {
  return list(v)
    .filter(isRec)
    .filter((u) => typeof u.id === "string")
    .map((u) => ({
      id: u.id as string,
      title: str(u.title),
      body: str(u.body),
      media_ids: Array.isArray(u.media_ids) ? u.media_ids.filter((id): id is string => typeof id === "string") : [],
      published_at: strOrNull(u.published_at ?? u.created_at),
    }));
}

/** `GET /public/sitemap` → `{campaigns: [{slug, updated_at}], categories: [{slug, updated_at}]}`. */
export function readSitemap(v: unknown): SitemapData {
  const d = isRec(v) ? v : {};
  const entries = (x: unknown) =>
    (Array.isArray(x) ? x : [])
      .filter(isRec)
      .filter((e) => typeof e.slug === "string" && CATEGORY_SLUG.test(e.slug) && e.slug.length <= 160)
      .map((e) => ({ slug: e.slug as string, updated_at: strOrNull(e.updated_at) }));
  return { campaigns: entries(d.campaigns), categories: entries(d.categories) };
}

/** `GET /public/campaign-codes/{code}` → `{canonical_slug}`. */
export function readCanonicalSlug(v: unknown): string | null {
  return isRec(v) && typeof v.canonical_slug === "string" && CATEGORY_SLUG.test(v.canonical_slug) ? v.canonical_slug : null;
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

export function readStaffReports(v: unknown): StaffReport[] {
  return list(v)
    .filter(isRec)
    .filter((r) => typeof r.id === "string" && typeof r.campaign_id === "string")
    .map((r) => ({
      id: r.id as string,
      campaign_id: r.campaign_id as string,
      campaign_title: str(r.campaign_title),
      campaign_slug: strOrNull(r.campaign_slug),
      campaign_status: str(r.campaign_status),
      status: str(r.status, "OPEN"),
      reason: str(r.reason),
      details: strOrNull(r.details),
      has_contact: r.has_contact === true,
      reporter_signed_in: r.reporter_signed_in === true,
      created_at: strOrNull(r.created_at),
      triaged_at: strOrNull(r.triaged_at),
      triage_reason_code: strOrNull(r.triage_reason_code),
      triage_note: strOrNull(r.triage_note),
    }));
}


/** `GET /campaign-locations` → provinces then towns (well-formed codes only). */
export function readLocationOptions(v: unknown): CampaignLocationOption[] {
  return list(v)
    .filter(isRec)
    .filter((l) => typeof l.code === "string" && /^(PROVINCE|TOWN)_[A-Z_]{2,40}$/.test(l.code) && typeof l.name === "string")
    .map((l) => ({ code: l.code as string, kind: str(l.kind), name: l.name as string, province_code: strOrNull(l.province_code) }));
}
