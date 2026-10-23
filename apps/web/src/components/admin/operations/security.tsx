"use client";

import Link from "next/link";

import { OpsAction } from "@/components/admin/operations/ops-action";
import { TableShell, When } from "@/components/admin/payments/common";
import { Badge, type BadgeTone } from "@/components/ui/badge";
import { humanise } from "@/lib/payments/labels";
import { opsApi } from "@/lib/operations/api";
import type { SecurityAlert } from "@/lib/operations/types";

/**
 * Stage 18 security alerts. Severity and state are written out (never colour alone). The API is the authority on every
 * action (permission, step-up, state, version); these components only ask.
 */

const row = "border-b border-line align-top last:border-b-0";
const cell = "p-3";

export const alertHref = (id: string) => `/admin/security/alerts/${encodeURIComponent(id)}`;

export function AlertSeverity({ severity }: { severity: string }) {
  const tone: BadgeTone = severity === "CRITICAL" || severity === "HIGH" ? "gold" : severity === "MEDIUM" ? "info" : "neutral";
  return <Badge tone={tone}>{humanise(severity)}</Badge>;
}

export function AlertsTable({ items, caption }: { items: SecurityAlert[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No alerts match.</p>;
  return (
    <TableShell caption={caption} head={["Alert", "Severity", "Status", "Condition", "Seen", "Last seen"]} minWidthClass="min-w-[52rem]">
      {items.map((a) => (
        <tr key={a.id} className={row}>
          <td className={`${cell} break-words`}>
            <Link href={alertHref(a.id)} className="font-semibold underline underline-offset-4">
              {a.title}
            </Link>
            <div className="text-sm text-ink-700">{a.alert_key}</div>
          </td>
          <td className={cell}>
            <AlertSeverity severity={a.severity} />
          </td>
          <td className={cell}>
            {humanise(a.status)}
            {a.escalation_status === "ESCALATED" ? " · escalated" : ""}
          </td>
          <td className={cell}>{a.condition_active ? "Present" : "Cleared"}</td>
          <td className={cell}>{a.occurrences === 1 ? "Once" : `${a.occurrences} times`}</td>
          <td className={cell}>
            <When at={a.last_seen_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

const NOTE = { kind: "textarea" as const, name: "note", label: "Note (staff only)", min: 5, max: 2000 };
const OPTIONAL_NOTE = { ...NOTE, min: 0, optional: true };

export function AlertActions({ alert, canManage }: { alert: SecurityAlert; canManage: boolean }) {
  if (!canManage) return <p className="text-ink-700">You can view this alert. Acting on it needs the security alert management permission.</p>;
  const run = (action: "acknowledge" | "escalate" | "resolve") => (b: Record<string, unknown>) => opsApi.alertAction(alert.id, action, b);
  return (
    <div className="flex flex-wrap gap-3">
      <OpsAction
        available={alert.status === "OPEN"}
        variant="primary"
        button="Acknowledge"
        title="Acknowledge this alert?"
        description="You take ownership of investigating it. The scan keeps updating whether the condition is present."
        confirm="Confirm: Acknowledge"
        notice="Alert acknowledged."
        fields={[OPTIONAL_NOTE]}
        body={{ version: alert.version }}
        run={run("acknowledge")}
      />
      <OpsAction
        available={alert.status === "ACKNOWLEDGED" && alert.escalation_status !== "ESCALATED"}
        button="Escalate to an incident"
        title="Escalate this alert?"
        description="Declares an operations incident for it (or links the open one). No notification leaves FundZim."
        confirm="Confirm: Escalate"
        notice="Alert escalated."
        fields={[NOTE]}
        body={{ version: alert.version }}
        run={run("escalate")}
      />
      <OpsAction
        available={alert.status === "ACKNOWLEDGED"}
        button="Resolve"
        title="Resolve this alert?"
        description={
          alert.condition_active
            ? "The condition is still present: resolving records your decision, and the next scan opens a new alert for it."
            : "The condition has cleared. Record what was done."
        }
        confirm="Confirm: Resolve"
        notice="Alert resolved."
        fields={[NOTE]}
        body={{ version: alert.version }}
        run={run("resolve")}
      />
    </div>
  );
}
