import { describe, expect, it } from "vitest";

import { readAgeAttestation, readCampaign, readCurrencies, readEligibility, readMedia, readNextCursor, readPublicCampaign, readPublicSummaries, readStaffDetail, readStaffLink, readUpdates } from "./normalise";

describe("campaign normalisers", () => {
  it("reads an owner campaign and keeps money as digit strings", () => {
    const c = readCampaign({ id: "c1", status: "DRAFT", goal: { amount_minor: "150000", currency: "USD" }, version: 3, eligibility: { SUBMIT_FOR_REVIEW: { allowed: false, reasons: [{ code: "COVER_IMAGE_REQUIRED" }] } } });
    expect(c.goal).toEqual({ amount_minor: "150000", currency: "USD" });
    expect(c.version).toBe(3);
    expect(c.eligibility).toEqual([{ action: "SUBMIT_FOR_REVIEW", allowed: false, reasons: [{ code: "COVER_IMAGE_REQUIRED", field: null, message: null }] }]);
    expect(readCampaign({ id: "c", goal: { amount_minor: 1500, currency: "USD" } }).goal).toBeNull();
  });

  it("reads eligibility as a single result, a list or a map", () => {
    expect(readEligibility({ action: "PUBLISH", allowed: true, reasons: [] })).toEqual([{ action: "PUBLISH", allowed: true, reasons: [] }]);
    expect(readEligibility([{ action: "X", allowed: false, reasons: [] }])).toHaveLength(1);
    expect(readEligibility(null)).toEqual([]);
  });

  it("keeps only available currencies", () => {
    expect(readCurrencies([
      { code: "USD", minor_units: 2, display_symbol: "US$" },
      { code: "ZWG", minor_units: 2, available: false },
      { code: "XXX", minor_units: 9 },
      { code: "usd", minor_units: 2 },
    ]).map((c) => c.code)).toEqual(["USD"]);
  });

  it("never discloses a beneficiary name when disclosure is NONE", () => {
    expect(readPublicCampaign({ beneficiary: { disclosure: "NONE", display_name: "Secret Person" } }).beneficiary).toEqual({ disclosure: "NONE", display_name: null });
    expect(readPublicCampaign({ beneficiary: { disclosure: "DISPLAY_NAME", display_name: "Chipo" } }).beneficiary.display_name).toBe("Chipo");
  });

  it("reads staff details with a generic restriction flag", () => {
    const d = readStaffDetail({ campaign: { id: "c1", status: "UNDER_REVIEW" }, review: { pending_outcome: "APPROVE", pending_decided_by: { id: "u1" } }, restricted: true });
    expect(d.review.pending_decided_by).toBe("u1");
    expect(d.restricted).toBe(true);
    expect(d.allowed_actions).toBeNull();
  });

  it("reads age attestation flat or nested", () => {
    expect(readAgeAttestation({ current: { outcome: "ATTESTED", attested_at: "t" }, adult_age: 18, statement_version: "v2" })).toMatchObject({ outcome: "ATTESTED", adult_age: 18, statement_version: "v2" });
    expect(readAgeAttestation({ outcome: null })).toMatchObject({ outcome: null, adult_age: 18 });
  });
});

describe("Go handler shapes (internal/campaigns, verification/age.go, auth/stafflink.go)", () => {
  it("owner view: feedback, re_review_pending, owner organisation", () => {
    const c = readCampaign({
      id: "c1", owner: { type: "ORGANISATION", id: "o1" }, category: "EDUCATION", goal: { amount_minor: "5000", currency: "USD" }, status: "CHANGES_REQUESTED",
      re_review_pending: true, feedback: { outcome: "CHANGES_REQUESTED", reason_code: "CONTENT_UNCLEAR", user_message: "Explain the costs.", decided_at: "t" }, version: 4,
      donations: { available: false, message: "Donations are not yet available." },
    });
    expect(c.organisation_id).toBe("o1");
    expect(c.re_review_required).toBe(true);
    expect(c.review_feedback).toEqual({ outcome: "CHANGES_REQUESTED", reason_code: "CONTENT_UNCLEAR", message: "Explain the costs.", decided_at: "t" });
    expect(c.eligibility).toEqual([]);
  });

  it("media list `{media}` with COVER/GALLERY kinds and rejected_reason", () => {
    const m = readMedia({ media: [{ id: "g", kind: "GALLERY", position: 2, alt_text: "b", status: "SCANNING" }, { id: "c", kind: "COVER", position: 0, alt_text: "a", status: "REJECTED", rejected_reason: "MALWARE_DETECTED" }] });
    expect(m.map((x) => [x.id, x.kind])).toEqual([["c", "COVER"], ["g", "GALLERY"]]);
    expect(m[0]!.rejection_reason).toBe("MALWARE_DETECTED");
  });

  it("updates list `{updates, next_cursor}`", () => {
    expect(readUpdates({ updates: [{ id: "u", title: "t", body: "b", status: "PENDING_MODERATION" }], next_cursor: null })[0]!.status).toBe("PENDING_MODERATION");
  });

  it("public view: category code, organiser, disclosed flag, cover and gallery ids", () => {
    const p = readPublicCampaign({
      slug: "s-1", category: "EDUCATION", organiser: { type: "ORGANISATION", display_name: "Mbare Trust" }, beneficiary: { disclosed: true, display_name: "Chipo" },
      cover_media_id: "m1", media_ids: ["m1", "m2"], goal: { amount_minor: "100", currency: "USD" }, donations: { available: false },
    });
    expect(p.category.code).toBe("EDUCATION");
    expect(p.organisation).toEqual({ display_name: "Mbare Trust" });
    expect(p.beneficiary).toEqual({ disclosure: "DISPLAY_NAME", display_name: "Chipo" });
    expect(p.cover?.id).toBe("m1");
    expect(p.gallery.map((g) => g.id)).toEqual(["m2"]);
    expect(readPublicCampaign({ beneficiary: { disclosed: false, display_name: "Leak" } }).beneficiary.display_name).toBeNull();
    expect(readPublicSummaries({ items: [{ slug: "a", cover_media_id: "m9", category: "MEDICAL" }], next_cursor: "x" })[0]!.cover?.id).toBe("m9");
    expect(readNextCursor({ items: [], next_cursor: "t|id" })).toBe("t|id");
    expect(readNextCursor({ items: [], next_cursor: null }, { next_cursor: "m" })).toBe("m");
  });

  it("staff detail: string assignee/pending ids, string restriction, beneficiary `type`, history event_type", () => {
    const d = readStaffDetail({
      campaign: { id: "c1", status: "UNDER_REVIEW" }, owner: { type: "USER", display_name: "Tendai" }, restriction: "",
      beneficiary: { id: "b", type: "SELF", display_name: "Chipo", verification_status: "APPROVED" },
      media: { CoverApproved: true, Pending: 0, Rejected: 0 },
      review: { assigned_to: "s1", pending_decided_by: "s1", requires_second_approval: true },
      history: [{ version: 2, event_type: "SUBMITTED", from_status: "DRAFT", to_status: "SUBMITTED", actor_type: "USER", occurred_at: "t" }],
      eligibility: { APPROVE: { action: "APPROVE", allowed: true, reasons: [] }, PUBLISH: { action: "PUBLISH", allowed: false, reasons: [{ code: "COVER_IMAGE_REQUIRED" }] } },
    });
    expect(d.campaign.owner_display_name).toBe("Tendai");
    expect(d.restricted).toBe(false);
    expect(readStaffDetail({ restriction: "SUSPENDED" }).restricted).toBe(true);
    expect(d.beneficiary?.beneficiary_type).toBe("SELF");
    expect(d.review).toMatchObject({ assigned_to: { id: "s1" }, pending_decided_by: "s1", pending_outcome: "APPROVE", requires_second_approval: true });
    expect(d.media).toEqual([]);
    expect(d.history[0]).toMatchObject({ action: "SUBMITTED", actor: { type: "USER" } });
    expect(d.eligibility.map((e) => e.action)).toEqual(["APPROVE", "PUBLISH"]);
  });

  it("age attestation `{attestation, level, basic_unmet}` and staff link `{linked, pending}`", () => {
    expect(readAgeAttestation({ adult_age: 18, statement_version: "2026-10-v1", attestation: { outcome: "ATTESTED", attested_at: "t", source: "DASHBOARD" }, level: "UNVERIFIED", basic_unmet: ["PHONE_NOT_VERIFIED"] })).toEqual({
      outcome: "ATTESTED", statement_version: "2026-10-v1", adult_age: 18, attested_at: "t", source: "DASHBOARD", level: "UNVERIFIED", basic_unmet: ["PHONE_NOT_VERIFIED"],
    });
    expect(readStaffLink({ linked: false, personal_email_masked: null, linked_at: null, pending: { email_masked: "t•••@x", expires_at: "e" } })).toMatchObject({ status: "PENDING", email_masked: "t•••@x", expires_at: "e" });
    expect(readStaffLink({ linked: true, personal_email_masked: "a•••@x", linked_at: "l", pending: null })).toMatchObject({ status: "LINKED", email_masked: "a•••@x" });
    expect(readStaffLink({ linked: false, pending: null }).status).toBe("NONE");
  });
});
