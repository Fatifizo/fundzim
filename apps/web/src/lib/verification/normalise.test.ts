import { describe, expect, it } from "vitest";

import { normaliseComplianceCase, normaliseComplianceList, normaliseQueue, normaliseReviewCase } from "./normalise";

describe("normaliseQueue", () => {
  it("reads the contract shape (array + meta.next_cursor)", () => {
    const out = normaliseQueue([{ id: "a", type: "KYC", status: "SUBMITTED", subject: { type: "USER", id: "u", display_name: "Chipo" }, assigned_to: null, risk_level: "STANDARD", submitted_at: "t", updated_at: "t" }], "c1");
    expect(out.nextCursor).toBe("c1");
    expect(out.items[0]).toMatchObject({ id: "a", subject: { display_name: "Chipo" }, assigned_to: null });
  });

  it("reads the backend shape ({items, next_cursor}, bare ids, awaiting flag)", () => {
    const out = normaliseQueue(
      {
        items: [
          { id: "b", type: "BENEFICIARY", status: "UNDER_REVIEW", subject_type: "BENEFICIARY", subject_id: "s1", risk_level: "ENHANCED", assigned_to: "me-1", submitted_at: "2026-10-09T08:00:00Z", requires_second_approval: true, awaiting_second_approval: true },
        ],
        next_cursor: "2026-10-09T08:00:00Z",
      },
      undefined,
      "me-1",
    );
    expect(out.nextCursor).toBe("2026-10-09T08:00:00Z");
    expect(out.items[0]).toMatchObject({ status: "AWAITING_SECOND_APPROVAL", subject: { type: "BENEFICIARY", id: "s1", display_name: null }, assigned_to: { id: "me-1", display_name: "You" }, requires_second_approval: true });
  });

  it("degrades to an empty list on garbage", () => {
    expect(normaliseQueue(null).items).toEqual([]);
    expect(normaliseQueue({ nope: 1 }).items).toEqual([]);
  });
});

describe("normaliseReviewCase", () => {
  it("reads the backend's nested detail (case view + subject summary + history items)", () => {
    const rc = normaliseReviewCase(
      {
        id: "k1",
        type: "KYC",
        case: {
          id: "k1",
          kind: "KYC",
          status: "UNDER_REVIEW",
          identity: { legal_first_name: "Chipo", id_document_number_masked: "••12" },
          documents: [{ id: "d1", status: "CLEAN" }],
          requirements: [{ document_type: "ZW_NATIONAL_ID", sides: ["FRONT"], satisfied: true }],
          information_requests: [],
          decision: null,
          submitted_at: "2026-10-09T08:00:00Z",
        },
        assigned_to: "staff-9",
        subject: { type: "USER", id: "u1", display_name: "Chipo Ncube", email_masked: "c***@example.test" },
        checks: [],
        history: [{ event_type: "SUBMITTED", from_status: "DRAFT", to_status: "SUBMITTED", actor_type: "USER", actor_id: "u1", reason_code: null, occurred_at: "2026-10-09T08:00:00Z" }],
        notes: [],
        risk: { rating: "ENHANCED", signals: [{ signal_type: "DUPLICATE_IDENTITY" }] },
      },
      "staff-1",
    );
    expect(rc).toMatchObject({
      id: "k1",
      type: "KYC",
      status: "UNDER_REVIEW",
      subject: { display_name: "Chipo Ncube" },
      assigned_to: { id: "staff-9", display_name: "Another staff member" },
      identity: { id_document_number_masked: "••12" },
      risk_level: "ENHANCED",
      risk: { level: "ENHANCED", signals: [{ code: "DUPLICATE_IDENTITY" }] },
      pending_approval: null,
    });
    expect(rc.documents).toHaveLength(1);
    expect(rc.history[0]).toMatchObject({ action: "SUBMITTED", actor: { type: "USER", display_name: null } });
    expect(rc.subject_summary).toEqual([{ label: "Account email", value: "c***@example.test" }]);
  });

  it("builds a masked summary for payout destinations (never a full number) and reads beneficiary verification", () => {
    const dest = normaliseReviewCase({ id: "p1", type: "PAYOUT_DESTINATION", status: "PENDING_VERIFICATION", case: { rail: "ECOCASH", masked_identifier: "••••4567", holder_name: "C N", currency: "USD", checks: { format_validated: true, ownership: "PROVIDER_CONFIRMATION_REQUIRED", compliance: "PENDING" } }, documents: [], history: [] });
    expect(dest.subject_summary).toContainEqual({ label: "Account", value: "••••4567" });
    expect(dest.subject_summary).toContainEqual({ label: "Ownership check", value: "PROVIDER_CONFIRMATION_REQUIRED" });
    const ben = normaliseReviewCase({ id: "b1", type: "BENEFICIARY", case: { display_name: "T", beneficiary_type: "MINOR", relationship: { type: "PARENT_GUARDIAN" }, verification: { status: "UNDER_REVIEW", requires_second_approval: true, information_requests: [], decision: null, risk_level: "ENHANCED" }, documents: [] }, history: [], awaiting_second_approval: true });
    expect(ben.status).toBe("UNDER_REVIEW");
    expect(ben.requires_second_approval).toBe(true);
    expect(ben.pending_approval).toMatchObject({ approved_by: { display_name: "Another reviewer" } });
  });

  it("reads the KYC/KYB `review` block (risk, four-eyes, pending first approval)", () => {
    const rc = normaliseReviewCase({ id: "k", type: "KYB", case: { status: "UNDER_REVIEW" }, history: [], review: { risk_level: "RESTRICTED", requires_second_approval: true, pending_outcome: "APPROVE", pending_decided_by: "s1" } }, "s1");
    expect(rc).toMatchObject({ risk_level: "RESTRICTED", requires_second_approval: true, status: "UNDER_REVIEW", pending_approval: { approved_by: { id: "s1", display_name: "You" }, outcome: "APPROVE" } });
    const none = normaliseReviewCase({ id: "k", type: "KYC", case: { status: "UNDER_REVIEW" }, history: [], review: { risk_level: "LOW", requires_second_approval: false, pending_outcome: null, pending_decided_by: null } }, "s1");
    expect(none.pending_approval).toBeNull();
    expect(none.risk_level).toBe("LOW");
  });

  it("passes the flat contract reading through", () => {
    const rc = normaliseReviewCase({ id: "x", type: "KYB", status: "SUBMITTED", subject: { type: "ORGANISATION", id: "o", display_name: "Trust" }, assigned_to: { id: "s", display_name: "Rev" }, documents: [], history: [{ id: "h", action: "kyb.case_submitted", actor: { type: "USER", display_name: "A" }, occurred_at: "t" }], allowed_actions: ["assign"] });
    expect(rc).toMatchObject({ status: "SUBMITTED", assigned_to: { display_name: "Rev" }, allowed_actions: ["assign"], history: [{ id: "h", actor: { display_name: "A" } }] });
  });
});

describe("compliance normalisers", () => {
  const backendCase = {
    id: "c1",
    case_number: "CC-0007",
    case_type: "KYC_REVIEW",
    severity: "S2",
    status: "RESOLVED",
    assigned_to: "me",
    opened_at: "2026-10-09T08:00:00Z",
    resolution: { decision: "RESTRICT", reason_code: "CONCERNS_CONFIRMED", status: "PROPOSED", requires_approval: true, decided_by: "me", decided_at: "t", approved_by: null, approved_at: null },
    links: [
      { id: "l1", subject_type: "USER", subject_id: "u1", role: "PRIMARY_SUBJECT", linked_at: "t" },
      { id: "l2", subject_type: "KYC_CASE", subject_id: "k1", role: "RELATED_OBJECT", linked_at: "t" },
    ],
    events: [{ id: "e1", event_type: "RESOLVED", actor_type: "STAFF", actor_id: "me", reason_code: "CONCERNS_CONFIRMED", occurred_at: "t" }],
    notes: [{ id: "n1", author_id: "me", visibility: "COMPLIANCE", body: "Checked.", created_at: "t" }],
  };

  it("reads case_number, bare-id assignee/authors, links and resolution.status", () => {
    const c = normaliseComplianceCase(backendCase, "me");
    expect(c).toMatchObject({
      reference: "CC-0007",
      assigned_to: { id: "me", display_name: "You" },
      subject: { type: "USER", id: "u1" },
      resolution: { decision: "RESTRICT", resolution_status: "PROPOSED", proposed_by: { id: "me" }, approved_by: null },
      notes: [{ author: { display_name: "You" }, body: "Checked." }],
      events: [{ action: "RESOLVED", reason_code: "CONCERNS_CONFIRMED" }],
    });
    expect(c.links).toEqual([
      { type: "USER", id: "u1", label: "PRIMARY_SUBJECT" },
      { type: "KYC_CASE", id: "k1", label: "RELATED_OBJECT" },
    ]);
  });

  it("lists in either envelope", () => {
    expect(normaliseComplianceList([backendCase], "n").items[0]!.reference).toBe("CC-0007");
    expect(normaliseComplianceList({ items: [backendCase], next_cursor: "z" }).nextCursor).toBe("z");
  });
});

describe("opaque queue cursor", () => {
  it("is passed back unchanged and URL-encoded", async () => {
    const { parseQueueFilters, queueApiPath } = await import("@/components/admin/review-queue");
    const cursor = "2026-10-09T08:00:00.123456789Z|0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
    const filters = parseQueueFilters({ cursor, type: "KYC" });
    expect(filters.cursor).toBe(cursor);
    const path = queueApiPath(filters);
    expect(path).toContain(`cursor=${encodeURIComponent(cursor)}`);
    expect(new URL(path, "http://x").searchParams.get("cursor")).toBe(cursor);
    expect(parseQueueFilters({ cursor: "bad\ncursor" }).cursor).toBeUndefined();
  });
});
