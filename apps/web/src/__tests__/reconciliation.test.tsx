import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ProposalForm, StatementUploadForm } from "@/components/admin/reconciliation/recon-admin";
import { readException, readImportDetail, readOverview, formatMinor } from "@/lib/reconciliation/admin";
import type { ReconciliationApi } from "@/lib/reconciliation/api";
import { checkStatement, statementType } from "@/lib/reconciliation/upload";
import { axeViolations } from "@/test/axe";

vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: vi.fn(), push: vi.fn() }) }));
vi.mock("@/components/auth/step-up", () => ({
  useStepUp: () => (fn: () => Promise<unknown>) => fn(),
  StepUpCancelledError: class extends Error {},
}));

const EXC = {
  id: "0192a000-0000-7000-8000-0000000000e1", exception_type: "UNKNOWN_PAYMENT_OUTCOME", severity: "HIGH", status: "INVESTIGATING",
  source_code: "sandbox_transactions_test", currency: "USD", occurrences: 2, item_id: "0192a000-0000-7000-8000-0000000000a1",
  subject_type: "PAYMENT", subject_id: "0192a000-0000-7000-8000-0000000000b1", expected_amount_minor: "1705", actual_amount_minor: "1705",
  detail: { reason: "the statement reports success", record_status: "UNKNOWN", nested: { x: 1 } }, version: 4, age_hours: 3,
  events: [{ id: "e1", event_type: "OPENED", to_status: "OPEN", actor_type: "SYSTEM", created_at: "2026-10-22T08:00:00Z" }],
  resolutions: [],
  item: { id: "0192a000-0000-7000-8000-0000000000a1", external_line_id: "P3", item_kind: "PAYMENT", direction: "CREDIT", line_status: "SUCCEEDED",
    amount_minor: "1705", currency: "USD", raw_currency_code: "USD", match_status: "EXCEPTION", match: null },
};

describe("reconciliation read models", () => {
  it("format integer minor units without floating point and keep negatives", () => {
    expect(formatMinor("1705", "USD")).toBe("USD 17.05");
    expect(formatMinor("123456789", "ZWG")).toBe("ZWG 1,234,567.89");
    expect(formatMinor("-15", "USD")).toBe("−USD 0.15");
    expect(formatMinor("5", "USD")).toBe("USD 0.05");
    expect(formatMinor("12.50", "USD")).toBe("—"); // never a decimal string
    expect(formatMinor(null, "USD")).toBe("—");
  });

  it("read exceptions defensively: unknown shapes become empty values, nested detail is dropped", () => {
    const e = readException(EXC);
    expect(e.type).toBe("UNKNOWN_PAYMENT_OUTCOME");
    expect(e.version).toBe(4);
    expect(e.line?.lineId).toBe("P3");
    expect(e.detail.find(([k]) => k === "nested")?.[1]).toBe("");
    const empty = readException({ amount: 1.5, expected_amount_minor: 12.5 });
    expect(empty.expected).toBeNull();
    expect(empty.events).toEqual([]);
  });

  it("overview keeps production gates and never invents freshness", () => {
    const o = readOverview({ production: { imports: "BLOCKED", settlement_posting: "BLOCKED" }, sources: [{ code: "x", source_kind: "BANK_STATEMENT" }],
      rule_set_status: "NOT_CONFIGURED" });
    expect(o.production).toEqual([["imports", "BLOCKED"], ["settlement_posting", "BLOCKED"]]);
    expect(o.sources[0]?.freshness).toBe("UNKNOWN");
    expect(o.ruleSet).toBeNull();
    expect(readImportDetail(null).lines).toEqual([]);
  });

  it("statement files are chosen by extension (CSV labels vary by operating system)", () => {
    const f = (name: string, size = 10, type = "") => new File([new Uint8Array(size)], name, { type });
    expect(statementType(f("s.CSV", 1, "application/vnd.ms-excel"))).toBe("text/csv");
    expect(statementType(f("s.json"))).toBe("application/json");
    expect(checkStatement(f("s.pdf"))).toBe("UNSUPPORTED_FILE_TYPE");
    expect(checkStatement(f("s.csv", 0))).toBe("EMPTY_FILE");
    expect(checkStatement(f("s.csv", 11 * 1024 * 1024))).toBe("FILE_TOO_LARGE");
  });
});

describe("reconciliation forms", () => {
  it("the upload form validates before sending and is accessible", async () => {
    const upload = vi.fn().mockResolvedValue({ id: "0192a000-0000-7000-8000-0000000000c1" });
    const { container } = render(<StatementUploadForm upload={upload} />);
    await userEvent.click(screen.getByRole("button", { name: "Import statement" }));
    expect(upload).not.toHaveBeenCalled();
    expect(screen.getByText("Choose the statement's source.")).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("Source"), "sandbox_settlement_test");
    await userEvent.upload(screen.getByLabelText(/Statement file/), new File(["record_type,ref"], "s.csv", { type: "text/csv" }));
    await userEvent.click(screen.getByRole("button", { name: "Import statement" }));
    expect(upload).toHaveBeenCalledWith(expect.objectContaining({ source: "sandbox_settlement_test", supersedesImportId: undefined }));
    expect(await screen.findByRole("link", { name: "View the import" })).toHaveAttribute("href", "/admin/reconciliation/batches/0192a000-0000-7000-8000-0000000000c1");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("a proposal needs a justification and evidence references, and sends the record version", async () => {
    const propose = vi.fn().mockResolvedValue({});
    const api = { propose } as unknown as ReconciliationApi;
    render(<ProposalForm exc={readException(EXC)} api={api} />);
    await userEvent.selectOptions(screen.getByLabelText("Resolution"), "PROVIDER_OUTCOME_APPLIED");
    await userEvent.click(screen.getByRole("button", { name: "Propose resolution" }));
    expect(propose).not.toHaveBeenCalled();
    expect(screen.getByText("Write 20 to 2,000 characters.")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/Justification/), "The TEST statement line reports success at the provider.");
    await userEvent.click(screen.getByRole("button", { name: "Propose resolution" }));
    expect(propose).toHaveBeenCalledWith(EXC.id, expect.objectContaining({ resolution_type: "PROVIDER_OUTCOME_APPLIED", version: 4,
      evidence_refs: [`item:${EXC.item_id}`] }), expect.any(String));
  });

  it("the proposal confirmation stays after the refresh closes the form", async () => {
    const propose = vi.fn().mockResolvedValue({});
    const api = { propose } as unknown as ReconciliationApi;
    const { rerender } = render(<ProposalForm exc={readException(EXC)} api={api} />);
    await userEvent.selectOptions(screen.getByLabelText("Resolution"), "PROVIDER_OUTCOME_APPLIED");
    await userEvent.type(screen.getByLabelText(/Justification/), "The TEST statement line reports success at the provider.");
    await userEvent.click(screen.getByRole("button", { name: "Propose resolution" }));
    expect(await screen.findByText(/second team member/)).toBeInTheDocument();
    rerender(<ProposalForm exc={readException(EXC)} open={false} api={api} />);
    expect(screen.queryByRole("button", { name: "Propose resolution" })).not.toBeInTheDocument();
    expect(screen.getByText(/second team member/)).toBeInTheDocument();
  });

  it("a ledger adjustment proposal parses journal lines as integer minor units", async () => {
    const propose = vi.fn().mockResolvedValue({});
    render(<ProposalForm exc={readException(EXC)} api={{ propose } as unknown as ReconciliationApi} />);
    await userEvent.selectOptions(screen.getByLabelText("Resolution"), "LEDGER_ADJUSTMENT");
    await userEvent.selectOptions(screen.getByLabelText("Ledger rule"), "SETTLEMENT_DISCREPANCY_RESOLVED");
    await userEvent.type(screen.getByLabelText(/Justification/), "Correcting a TEST discrepancy with a balanced journal.");
    await userEvent.type(screen.getByLabelText("Journal lines"), "suspense:settlement_discrepancy:x, debit, 12.50{enter}asset:psp_settled:x, CREDIT, 1250");
    await userEvent.click(screen.getByRole("button", { name: "Propose resolution" }));
    expect(propose).not.toHaveBeenCalled(); // "12.50" is not minor units
    await userEvent.clear(screen.getByLabelText("Journal lines"));
    await userEvent.type(screen.getByLabelText("Journal lines"), "suspense:settlement_discrepancy:x, debit, 1250{enter}asset:psp_settled:x, CREDIT, 1250");
    await userEvent.click(screen.getByRole("button", { name: "Propose resolution" }));
    expect(propose).toHaveBeenCalledWith(EXC.id, expect.objectContaining({ posting_rule: "SETTLEMENT_DISCREPANCY_RESOLVED", lines: [
      { account_code: "suspense:settlement_discrepancy:x", direction: "DEBIT", amount_minor: "1250" },
      { account_code: "asset:psp_settled:x", direction: "CREDIT", amount_minor: "1250" }] }), expect.any(String));
  });
});
