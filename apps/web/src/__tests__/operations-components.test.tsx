import { readFileSync } from "node:fs";
import { join } from "node:path";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { OpsAction } from "@/components/admin/operations/ops-action";
import { OpenSupportCaseForm, ProposeSLAForm } from "@/components/admin/operations/ops-forms";
import { ApprovalsTable, MetricsGrid, SourcesNotice } from "@/components/admin/operations/ops";
import { TasksIndicator } from "@/components/admin/admin-shell-client";
import { StepUpProvider } from "@/components/auth/step-up";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import type { OpsApi } from "@/lib/operations/api";
import { describeOpsError } from "@/lib/operations/errors";
import { breadcrumbs, can, NAV_GROUPS, visibleNav } from "@/lib/operations/navigation";
import { formatDuration, items, metrics } from "@/lib/operations/normalise";
import { INCIDENT_EDGES, SUPPORT_CATEGORIES, SUPPORT_EDGES, TRIAGE_EDGES, type Approval, type Metric } from "@/lib/operations/types";
import { RISK_ACTION_PERMISSION } from "@/lib/risk/permissions";
import { axeViolations } from "@/test/axe";

// Stage 14 operations portal UI: permission-aware navigation and actions (presentation only — the API authorizes),
// metrics with sources and natures (never a missing value shown as 0), approvals routed to the owning module.

function apiError(code: string, status: number, details: Array<{ field: string; code: string }> = []) {
  return new ApiError({ code, status, message: "server text that must not be shown", retryable: false, details });
}

function fakeAuth(stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-16T10:00:00Z" }))): AuthApi {
  return new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : vi.fn()) }) as AuthApi;
}

function fakeOps(overrides: Partial<Record<keyof OpsApi, unknown>> = {}): OpsApi {
  return new Proxy({}, { get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => undefined) }) as OpsApi;
}

const shell = (ui: ReactNode, auth: AuthApi = fakeAuth()) => (
  <StepUpProvider mfaEnabled codeOnly api={auth}>
    {ui}
  </StepUpProvider>
);

const SUPPORT = ["ops.dashboard.view", "ops.incident.view", "ops.support.view", "ops.support.work", "user.view", "campaign.view", "payment.view"];
const FINANCE = ["ops.dashboard.view", "ops.exception.work", "ledger.view", "payout.view", "payment.view", "fee.view", "risk.operations.view", "audit.read"];

describe("permission-aware navigation", () => {
  it("shows only the pages a role can open", () => {
    const labels = (perms: string[]) => visibleNav(perms).flatMap((g) => g.items.map((i) => i.label));
    const support = labels(SUPPORT);
    expect(support).toContain("Support cases");
    expect(support).toContain("Command center");
    expect(support).not.toContain("Ledger");
    expect(support).not.toContain("Risk cases");
    expect(support).not.toContain("Identity (KYC)");
    expect(support).not.toContain("Audit log");
    expect(support).not.toContain("Staff directory");
    const finance = labels(FINANCE);
    expect(finance).toContain("Ledger operations");
    expect(finance).toContain("Audit log");
    expect(finance).not.toContain("Support cases");
    expect(finance).not.toContain("Staff directory");
  });

  it("fails closed without permissions and drops empty groups", () => {
    const groups = visibleNav(undefined);
    expect(groups.map((g) => g.id)).toEqual(["account"]);
    expect(can(undefined, "ops.dashboard.view")).toBe(false);
    expect(can([], null)).toBe(true);
  });

  it("names a permission for every staff page except the personal link", () => {
    for (const g of NAV_GROUPS)
      for (const i of g.items) {
        expect(i.href.startsWith("/admin")).toBe(true);
        if (i.href !== "/admin/account/personal-link") expect(i.permission).toMatch(/^[a-z_]+(\.[a-z_]+)+$/);
      }
  });

  it("links breadcrumbs to pages only and shortens record ids", () => {
    expect(breadcrumbs("/admin/finance/withdrawals/0192a000-0000-7000-8000-0000000000a1")).toEqual([
      { href: "/admin", label: "Staff" },
      { href: null, label: "Finance" },
      { href: "/admin/finance/withdrawals", label: "withdrawals" },
      { href: null, label: "Record 0192a000" },
    ]);
    expect(breadcrumbs("/admin/support")[1]).toEqual({ href: "/admin/support", label: "Support cases" });
    expect(breadcrumbs("/dashboard")).toEqual([]);
  });

  it("maps every risk action to the permission its route requires (KI-S13-21)", () => {
    const go = readFileSync(join(process.cwd(), "../../internal/risk/http.go"), "utf8");
    const routePerm = (suffix: string) => {
      const m = new RegExp(`"/${suffix}", Policy: perm\\("([a-z._]+)"\\)`).exec(go);
      return m?.[1];
    };
    expect(RISK_ACTION_PERMISSION["rule-approve"]).toBe(routePerm("approve"));
    expect(RISK_ACTION_PERMISSION["case-approve"]).toBe(routePerm("approve-decision"));
    expect(RISK_ACTION_PERMISSION["hold-approve-release"]).toBe(routePerm("approve-release"));
    expect(RISK_ACTION_PERMISSION["case-escalate"]).toBe(routePerm("escalate"));
  });
});

describe("lifecycles mirror the backend", () => {
  // the Go maps are the authority (and the database guards); the UI only offers what they allow
  const goMap = (file: string, name: string) => {
    const src = readFileSync(join(process.cwd(), "../../internal/operations", file), "utf8");
    const body = new RegExp(`var ${name} = map\\[string\\]\\[\\]string\\{([\\s\\S]*?)\\n\\}`).exec(src)?.[1] ?? "";
    const out: Record<string, string[]> = {};
    for (const m of body.matchAll(/"([A-Z_]*)":\s*\{([^}]*)\}/g)) if (m[1]) out[m[1]] = [...m[2]!.matchAll(/"([A-Z_]+)"/g)].map((x) => x[1]!);
    return out;
  };
  it("support, incident and exception edges equal the Go edge lists", () => {
    expect(SUPPORT_EDGES).toEqual(goMap("support.go", "SupportEdges"));
    const incidents = { ...INCIDENT_EDGES } as Record<string, readonly string[]>;
    delete incidents.CLOSED; // terminal: no entry in Go
    expect(incidents).toEqual(goMap("incidents.go", "IncidentEdges"));
    const triage = { ...TRIAGE_EDGES } as Record<string, readonly string[]>;
    delete triage.UNTRIAGED; // the UI's name for "no triage record yet" (first touch opens and acknowledges)
    expect(triage).toEqual(goMap("exceptions.go", "ExceptionEdges"));
  });
  it("support categories equal the Go list", () => {
    const src = readFileSync(join(process.cwd(), "../../internal/operations/support.go"), "utf8");
    const body = /var supportCategories = map\[string\]bool\{([^}]*)\}/.exec(src)?.[1] ?? "";
    expect([...SUPPORT_CATEGORIES].sort()).toEqual([...body.matchAll(/"([A-Z_]+)"/g)].map((m) => m[1]).sort());
  });
});

describe("normalisers and copy", () => {
  it("keep a missing value missing, never zero", () => {
    const ms = metrics({ metrics: [{ key: "a", label: "A", module: "ledger", nature: "BLOCKED", source: "constant", permission: "ledger.view" }] });
    expect(ms[0]!.value).toBeNull();
    expect(items(null)).toEqual([]);
    expect(items({ items: "nope" })).toEqual([]);
  });
  it("format durations in whole units", () => {
    expect(formatDuration(45)).toBe("45 s");
    expect(formatDuration(3 * 3600 + 5 * 60)).toBe("3 h 5 min");
    expect(formatDuration(93_600)).toBe("1 d 2 h");
    expect(formatDuration(null)).toBe("—");
    expect(formatDuration(-1)).toBe("—");
  });
  it("map error codes to staff copy, never server text", () => {
    expect(describeOpsError(apiError("OPS_EXCEPTION_DOMAIN_OPEN", 409)).message).toMatch(/still reports this exception/);
    expect(describeOpsError(apiError("OPS_RECORD_CHANGED", 409)).message).toMatch(/changed this record/);
    expect(describeOpsError(apiError("VALIDATION_FAILED", 422, [{ field: "assignee_id", code: "NOT_ELIGIBLE" }]))).toEqual({
      message: expect.stringMatching(/cannot be assigned/),
      field: "assignee_id",
    });
    expect(describeOpsError(apiError("SOMETHING_NEW", 500)).message).not.toMatch(/server text/);
  });
});

const metric = (m: Partial<Metric>): Metric => ({ key: "k", label: "Label", module: "risk", value: 3, nature: "REAL", status: null, source: "risk.risk_cases open", permission: "risk.operations.view", href: null, ...m });

describe("operations components", () => {
  it("show each metric's source and nature; an unmeasured metric is 'Not measured'", async () => {
    const { container } = render(
      <MetricsGrid
        metrics={[
          metric({ key: "a", label: "Open risk cases" }),
          metric({ key: "b", label: "Live payment execution", module: "payments", value: null, nature: "BLOCKED", status: "BLOCKED", source: "constant psp.LiveExecutionEnabled" }),
          metric({ key: "c", label: "TEST payments unknown", module: "payments", value: 0, nature: "TEST_ONLY", source: "app.payments UNKNOWN" }),
        ]}
      />,
    );
    expect(screen.getByText("Source: risk.risk_cases open")).toBeInTheDocument();
    const live = screen.getByText("Live payment execution").closest("li")!;
    expect(within(live).getByText("Not measured")).toBeInTheDocument();
    expect(within(live).getAllByText("Blocked").length).toBeGreaterThan(0);
    expect(within(screen.getByText("TEST payments unknown").closest("li")!).getByText("TEST only")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("say which modules could not be read instead of showing zero", () => {
    render(<SourcesNotice sources={[{ module: "ledger", status: "UNAVAILABLE", error: "timeout" }, { module: "risk", status: "HEALTHY", error: null }]} />);
    expect(screen.getByText(/missing from this page \(not zero\)/)).toBeInTheDocument();
    expect(screen.getByText(/Ledger: Unavailable/)).toBeInTheDocument();
    expect(screen.queryByText(/Risk:/)).toBeNull();
  });

  it("route approvals to the owning module and explain why a maker cannot decide", () => {
    const base: Approval = { id: "risk:1", source_module: "risk", source_record_id: "1", action: "RULE_VERSION_APPROVAL", summary: "Rule version", requested_by: "0192a000-0000-7000-8000-0000000000a1",
      requested_at: "2026-10-15T10:00:00Z", required_permission: "risk.rule.approve", status: "PENDING", risk_level: null, href: "/admin/risk/rules/r1", can_decide: true, blocked_reason: null, age_seconds: 7200 };
    render(<ApprovalsTable caption="Pending" items={[base, { ...base, id: "risk:2", can_decide: false, blocked_reason: "MAKER" }]} />);
    expect(screen.getByRole("link", { name: "Review and decide in Risk" })).toHaveAttribute("href", "/admin/risk/rules/r1");
    expect(screen.getByText(/You made or requested this/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /approve/i })).toBeNull(); // no decision button here: the module decides
  });

  it("an action validates, sends its body with the record version and steps up when asked", async () => {
    const user = userEvent.setup();
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-16T10:00:00Z" }));
    const run = vi.fn().mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403)).mockResolvedValueOnce({});
    render(
      shell(
        <OpsAction button="Change status" title="Change status?" description="d" confirm="Confirm: Change" notice="Changed." body={{ version: 4 }} run={run}
          fields={[{ kind: "select", name: "to", label: "New status", options: ["IN_PROGRESS"] }, { kind: "textarea", name: "note", label: "Note (staff only)" }]} />,
        fakeAuth(stepUp),
      ),
    );
    await user.click(screen.getByRole("button", { name: "Change status" }));
    const dialog = screen.getByRole("dialog", { name: "Change status?" });
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Change" }));
    expect(run).not.toHaveBeenCalled();
    expect(within(dialog).getByText("Choose an option.")).toBeInTheDocument();
    await user.selectOptions(within(dialog).getByLabelText("New status"), "IN_PROGRESS");
    await user.type(within(dialog).getByLabelText("Note (staff only)"), "Working on it now");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Change" }));
    const sd = await screen.findByRole("dialog", { name: "Confirm it's you" });
    await user.type(within(sd).getByLabelText("Authentication code"), "123456");
    await user.click(within(sd).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(run).toHaveBeenCalledTimes(2));
    expect(run).toHaveBeenLastCalledWith({ version: 4, to: "IN_PROGRESS", note: "Working on it now" });
    expect(await screen.findByText("Changed.")).toBeInTheDocument();
  });

  it("a refused action shows staff copy and keeps the dialog open", async () => {
    const user = userEvent.setup();
    const run = vi.fn().mockRejectedValue(apiError("OPS_EXCEPTION_DOMAIN_OPEN", 409));
    render(shell(<OpsAction button="Resolve" title="Resolve?" description="d" confirm="Confirm: Resolve" notice="Resolved." run={run} />));
    await user.click(screen.getByRole("button", { name: "Resolve" }));
    await user.click(screen.getByRole("button", { name: "Confirm: Resolve" }));
    expect(await screen.findByText(/still reports this exception/)).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Resolve?" })).toBeInTheDocument();
    expect(screen.queryByText("Resolved.")).toBeNull();
  });

  it("the support form validates ids and sends one idempotency key per body", async () => {
    const user = userEvent.setup();
    const open = vi.fn(async () => ({ id: "0192a000-0000-7000-8000-0000000000c1" }));
    const opened = vi.fn();
    render(shell(<OpenSupportCaseForm api={fakeOps({ openSupportCase: open })} onOpened={opened} />));
    await user.type(screen.getByLabelText("Subject"), "Donor asks about a TEST donation");
    await user.selectOptions(screen.getByLabelText("Category"), "DONATION");
    await user.type(screen.getByLabelText("Requester account id (optional)"), "not-an-id");
    await user.click(screen.getByRole("button", { name: "Open support case" }));
    expect(open).not.toHaveBeenCalled();
    expect(screen.getAllByText("Paste a full record id.").length).toBeGreaterThan(0);
    await user.clear(screen.getByLabelText("Requester account id (optional)"));
    await user.click(screen.getByRole("button", { name: "Open support case" }));
    await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
    const call = open.mock.calls[0] as unknown as [Record<string, string>, string];
    expect(call[0]).toEqual({ subject: "Donor asks about a TEST donation", category: "DONATION", priority: "P3" });
    expect(call[1]).toMatch(/^[0-9a-f-]{36}$/);
    expect(opened).toHaveBeenCalledWith("0192a000-0000-7000-8000-0000000000c1");
  });

  it("the SLA form proposes whole hours as seconds and says it is an internal target", async () => {
    const user = userEvent.setup();
    const propose = vi.fn(async () => ({}));
    render(shell(<ProposeSLAForm api={fakeOps({ proposeSLA: propose })} />));
    expect(screen.getByText(/not a contractual or regulatory deadline/)).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Metric"), "SUPPORT_RESOLUTION");
    await user.type(screen.getByLabelText("Target (hours)"), "72");
    await user.type(screen.getByLabelText("Reason"), "Agreed internal support target");
    await user.click(screen.getByRole("button", { name: "Propose target" }));
    await waitFor(() => expect(propose).toHaveBeenCalled());
    expect((propose.mock.calls[0] as unknown as [unknown])[0]).toEqual({ metric_key: "SUPPORT_RESOLUTION", target_seconds: 259_200, reason: "Agreed internal support target" });
    expect(await screen.findByText(/only after a different person approves it/)).toBeInTheDocument();
  });

  it("the task indicator says when it could not load instead of showing zero", async () => {
    render(<TasksIndicator api={fakeOps({ tasks: vi.fn(async () => Promise.reject(new Error("down"))) })} />);
    expect(await screen.findByText("My tasks (could not load)")).toBeInTheDocument();
    render(<TasksIndicator api={fakeOps({ tasks: vi.fn(async () => ({ approvals_to_decide: 2, support_cases_assigned: 1, open_incidents: 9 })) })} />);
    expect(await screen.findByText("My tasks: 3")).toBeInTheDocument(); // open incidents are context, not the caller's tasks
  });
});
