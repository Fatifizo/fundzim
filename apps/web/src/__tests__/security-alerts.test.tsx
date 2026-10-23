import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AlertActions, AlertsTable } from "@/components/admin/operations/security";
import { StepUpProvider } from "@/components/auth/step-up";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import { describeOpsError } from "@/lib/operations/errors";
import { can, visibleNav } from "@/lib/operations/navigation";
import type { SecurityAlert } from "@/lib/operations/types";
import { axeViolations } from "@/test/axe";

vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: vi.fn() }) }));

const base: SecurityAlert = {
  id: "0192a000-0000-7000-8000-00000000aa01", alert_key: "BREAK_GLASS_ACTIVE", dedupe_key: "BREAK_GLASS_ACTIVE:x", severity: "HIGH",
  source_module: "identity_security", component: "auth.security.break_glass_active", title: "A break-glass grant is active", source: "app.break_glass_grants",
  evidence: { value: 1 }, status: "OPEN", condition_active: true, occurrences: 2, first_seen_at: "2026-10-23T08:00:00Z", last_seen_at: "2026-10-23T09:00:00Z",
  cleared_at: null, acknowledged_by: null, acknowledged_at: null, escalation_status: "NONE", incident_id: null, resolved_by: null, resolved_at: null,
  resolution_note: null, version: 3,
};
const auth = new Proxy({}, { get: () => vi.fn() }) as AuthApi;
const shell = (ui: React.ReactNode) => <StepUpProvider mfaEnabled codeOnly api={auth}>{ui}</StepUpProvider>;

// Stage 18: the alert queue writes severity and state out; actions follow the alert's state; navigation follows the
// permission (presentation only — the API authorizes every action).
describe("security alerts", () => {
  it("lists alerts with severity, state and occurrences as text", async () => {
    const { container } = render(<AlertsTable items={[base, { ...base, id: "0192a000-0000-7000-8000-00000000aa02", severity: "INFO", condition_active: false, occurrences: 1 }]} caption="Security alerts" />);
    expect(screen.getByRole("table", { name: "Security alerts" })).toHaveTextContent("High");
    expect(screen.getByText("2 times")).toBeInTheDocument();
    expect(screen.getByText("Cleared")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("offers only the actions the state allows", () => {
    const { rerender } = render(shell(<AlertActions alert={base} canManage />));
    expect(screen.getByRole("button", { name: "Acknowledge" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Resolve" })).not.toBeInTheDocument();
    rerender(shell(<AlertActions alert={{ ...base, status: "ACKNOWLEDGED" }} canManage />));
    expect(screen.queryByRole("button", { name: "Acknowledge" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Escalate to an incident" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Resolve" })).toBeInTheDocument();
    rerender(shell(<AlertActions alert={{ ...base, status: "ACKNOWLEDGED", escalation_status: "ESCALATED" }} canManage />));
    expect(screen.queryByRole("button", { name: "Escalate to an incident" })).not.toBeInTheDocument();
    rerender(shell(<AlertActions alert={base} canManage={false} />));
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.getByText(/security alert management permission/)).toBeInTheDocument();
  });

  it("navigation shows the queue only with security.alert.view; errors have copy", () => {
    const has = (perms: string[]) => visibleNav(perms).some((g) => g.items.some((i) => i.href === "/admin/security"));
    expect(has(["security.alert.view"])).toBe(true);
    expect(has(["ops.dashboard.view"])).toBe(false);
    expect(can(["security.alert.manage"], "security.alert.manage")).toBe(true);
    const e = new ApiError({ code: "SECURITY_ALERT_STATE", status: 409, message: "server text", retryable: false });
    expect(describeOpsError(e).message).toMatch(/acknowledge first/);
  });
});
