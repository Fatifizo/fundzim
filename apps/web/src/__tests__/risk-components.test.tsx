import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { RiskAction } from "@/components/admin/risk/risk-action";
import { HoldsTable, RiskBanner, ScreeningsTable, SignalsTable } from "@/components/admin/risk/risk";
import { StepUpProvider } from "@/components/auth/step-up";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import type { RiskApi } from "@/lib/risk/api";
import { describeRiskError } from "@/lib/risk/errors";
import { caseTransitions, durationText, holdActions, outcomeLabel, ruleActions, screeningStatusLabel } from "@/lib/risk/labels";
import { readHold, readRiskOverview, readScreening, readSignal } from "@/lib/risk/normalise";
import { axeViolations } from "@/test/axe";

// Stage 13 staff risk UI. Wording rules (ADR-045): a score is never a finding; a provider "no match" is never a clearance.

function apiError(code: string, status: number) {
  return new ApiError({ code, status, message: "server text that must not be shown", retryable: false, details: [] });
}

function fakeApi(overrides: Partial<Record<keyof RiskApi, unknown>> = {}): RiskApi {
  return new Proxy({}, { get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => undefined) }) as RiskApi;
}

function fakeAuth(stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-16T10:00:00Z" }))): AuthApi {
  return new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : vi.fn()) }) as AuthApi;
}

const shell = (ui: ReactNode, auth: AuthApi = fakeAuth()) => (
  <StepUpProvider mfaEnabled codeOnly api={auth}>
    {ui}
  </StepUpProvider>
);

const ID = "0192a000-0000-7000-8000-0000000000a1";
const HASH = "b".repeat(64);
const FINDING_WORDS = /\b(fraudster|fraudulent|sanctioned|guilty)\b/i;

describe("risk labels and normalisers", () => {
  it("never present a provider no-match or an error as a clearance", () => {
    expect(screeningStatusLabel("NO_MATCH_REPORTED")).toMatch(/not a clearance/);
    expect(screeningStatusLabel("ERROR")).toMatch(/not cleared/);
    expect(screeningStatusLabel("POTENTIAL_MATCH")).toMatch(/analyst review required/);
    for (const s of ["PENDING", "POTENTIAL_MATCH", "NO_MATCH_REPORTED", "REVIEW_REQUIRED", "CONFIRMED_MATCH", "CLEARED", "ERROR"]) {
      expect(screeningStatusLabel(s)).not.toMatch(FINDING_WORDS);
    }
  });

  it("offer only the lifecycle actions the backend allows", () => {
    expect(ruleActions("DRAFT")).toEqual(["submit", "retire"]);
    expect(ruleActions("PENDING_APPROVAL")).toEqual(["approve", "reject"]);
    expect(ruleActions("REJECTED")).toEqual([]);
    expect(ruleActions("RETIRED")).toEqual([]);
    expect(holdActions("REQUESTED")).toEqual(["activate", "revoke"]);
    expect(holdActions("RELEASE_PENDING")).toEqual(["approve-release", "reject-release"]);
    expect(holdActions("RELEASED")).toEqual([]);
    expect(caseTransitions("TRIAGE")).toEqual(["UNDER_INVESTIGATION"]);
    expect(caseTransitions("DECISION_PENDING")).toEqual([]); // decisions have their own maker-checker actions
    expect(outcomeLabel("FALSE_POSITIVE")).toBe("False positive");
    expect(durationText(604_800)).toBe("7 days");
    expect(durationText(3_600)).toBe("1 hour");
    expect(durationText(null)).toBe("No expiry");
  });

  it("force TEST labels and tolerate partial payloads", () => {
    const s = readScreening({ id: ID, status: "NO_MATCH_REPORTED", environment: "PRODUCTION", label: "REAL" });
    expect(s?.environment).toBe("TEST");
    expect(s?.label).toBe("TEST_ONLY");
    expect(readScreening({})).toBeNull();
    expect(readSignal({ nope: true })).toBeNull();
    expect(readHold({ id: ID, hold_type: "WITHDRAWAL_HOLD", status: "ACTIVE" })?.status).toBe("ACTIVE");
    const o = readRiskOverview({ screening_production: "ENABLED", accuracy_metrics: "99%" });
    expect(o.screening_production).toBe("BLOCKED");
    expect(o.accuracy_metrics).toBe("NOT_MEASURED");
  });

  it("map error codes without echoing server text", () => {
    expect(describeRiskError(apiError("RISK_CONFLICT_OF_INTEREST", 403)).message).toMatch(/concerns you or an account linked/);
    expect(describeRiskError(apiError("SCREENING_UNAVAILABLE", 503)).message).toMatch(/no screening provider is selected/);
    expect(describeRiskError(apiError("SOMETHING_NEW", 500)).message).not.toContain("server text");
  });
});

describe("risk components", () => {
  it("banner and tables say TEST, never call anyone a finding, and pass axe", async () => {
    const { container } = render(
      <div>
        <RiskBanner />
        <SignalsTable
          caption="Signals"
          items={[{ id: ID, signal_type: "CAMPAIGN_REPORTED", category: "CAMPAIGN_RISK", severity: "MEDIUM", confidence: 60, kind: "OBSERVED", subject_type: "CAMPAIGN",
            subject_id: ID, source_module: "campaigns", source_event_id: ID, observed_at: "2026-10-16T08:00:00Z", recorded_at: "2026-10-16T08:00:01Z",
            facts: { reason_code: "MISLEADING" }, review_classifications: ["FALSE_POSITIVE"] }]}
        />
        <ScreeningsTable caption="Screenings" items={[readScreening({ id: ID, subject_type: "USER", subject_id: ID, status: "NO_MATCH_REPORTED", synthetic_reference: "TEST-NO_MATCH-ABC" })!]} />
        <HoldsTable caption="Holds" items={[readHold({ id: ID, hold_type: "WITHDRAWAL_HOLD", subject_type: "CAMPAIGN", subject_id: ID, status: "ACTIVE", automated: true, expires_at: "2026-10-17T08:00:00Z", reason_code: "RULE_X" })!]} />
      </div>,
    );
    expect(container).toHaveTextContent("never proof of fraud");
    expect(container).toHaveTextContent("production screening BLOCKED");
    expect(container).toHaveTextContent(/not a clearance/);
    expect(container).toHaveTextContent("False positive");
    expect(container.textContent ?? "").not.toMatch(FINDING_WORDS);
    expect(await axeViolations(container)).toEqual([]);
  });

  it("rule approval requires a reason, binds the definition hash and steps up", async () => {
    const user = userEvent.setup();
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-16T10:00:00Z" }));
    const ruleAction = vi.fn().mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403)).mockResolvedValueOnce({});
    render(shell(<RiskAction target={{ kind: "rule-approve", id: ID, definitionHash: HASH }} api={fakeApi({ ruleAction })} />, fakeAuth(stepUp)));
    await user.click(screen.getByRole("button", { name: "Approve version" }));
    const dialog = screen.getByRole("dialog", { name: "Approve this rule version?" });
    expect(await axeViolations(dialog)).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    expect(within(dialog).getByText("Write 5 to 2,000 characters.")).toBeInTheDocument();
    expect(ruleAction).not.toHaveBeenCalled();
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Definition reviewed.");
    expect(within(dialog).getByLabelText(/Bind my decision/)).toBeChecked();
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    await user.type(await screen.findByLabelText("Authentication code"), "246810");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(ruleAction).toHaveBeenCalledTimes(2));
    expect(ruleAction).toHaveBeenLastCalledWith(ID, "approve", "Definition reviewed.", HASH);
    expect(await screen.findByRole("status")).toHaveTextContent("Version approved.");
  });

  it("a hold release by the requester is refused with mapped copy; the notice survives losing the action", async () => {
    const user = userEvent.setup();
    const holdAction = vi.fn().mockRejectedValueOnce(apiError("SELF_APPROVAL_FORBIDDEN", 403)).mockResolvedValueOnce({});
    const { rerender } = render(shell(<RiskAction target={{ kind: "hold-approve-release", id: ID }} api={fakeApi({ holdAction })} />));
    await user.click(screen.getByRole("button", { name: /approve release/i }));
    const dialog = screen.getByRole("dialog");
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Review completed.");
    await user.click(within(dialog).getByRole("button", { name: /^Confirm/ }));
    expect(await within(dialog).findByText(/You can't decide this/)).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("server text that must not be shown");
    await user.click(within(dialog).getByRole("button", { name: /^Confirm/ }));
    await waitFor(() => expect(holdAction).toHaveBeenLastCalledWith(ID, "approve-release", "Review completed."));
    expect(await screen.findByRole("status")).toBeInTheDocument();
    rerender(shell(<RiskAction target={{ kind: "hold-approve-release", id: ID }} available={false} api={fakeApi({ holdAction })} />));
    expect(screen.getByRole("status")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /approve release/i })).not.toBeInTheDocument();
  });

  it("proposing a case outcome offers only non-finding outcomes", async () => {
    const user = userEvent.setup();
    const caseAction = vi.fn(async () => ({}));
    render(shell(<RiskAction target={{ kind: "case-propose", id: ID }} api={fakeApi({ caseAction })} />));
    await user.click(screen.getByRole("button", { name: "Propose outcome" }));
    const dialog = screen.getByRole("dialog", { name: "Propose an outcome for this case?" });
    const options = within(dialog).getAllByRole("option").map((o) => o.textContent ?? "");
    expect(options.length).toBe(4);
    for (const o of options) expect(o).not.toMatch(/fraud/i);
    await user.selectOptions(within(dialog).getByLabelText("Outcome"), "FALSE_POSITIVE");
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Reports were synthetic.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Propose" }));
    await waitFor(() => expect(caseAction).toHaveBeenCalledWith(ID, "propose-decision", { outcome: "FALSE_POSITIVE", reason: "Reports were synthetic." }));
  });
});
