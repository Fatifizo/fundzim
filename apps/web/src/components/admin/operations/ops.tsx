import Link from "next/link";
import type { ReactNode } from "react";

import { Code, Section, TableShell, When } from "@/components/admin/payments/common";
import { Alert as Callout } from "@/components/ui/alert";
import { Badge, type BadgeTone } from "@/components/ui/badge";
import { humanise } from "@/lib/payments/labels";
import { formatDuration } from "@/lib/operations/normalise";
import type {
  Alert,
  Approval,
  AuditRecord,
  ExceptionItem,
  HistoryEntry,
  Incident,
  Metric,
  SourceStatus,
  StaffMember,
  SupportCase,
} from "@/lib/operations/types";

/**
 * Operations portal building blocks (Stage 14). Every value comes from the API; meaning is always carried by text
 * (status and nature are written out), never by colour alone.
 */

const row = "border-b border-line align-top last:border-b-0";
const cell = "p-3";

const NATURE_LABEL: Record<string, string> = {
  REAL: "Recorded",
  SIMULATED: "Simulated",
  TEST_ONLY: "TEST only",
  UNKNOWN: "Unknown",
  NOT_AVAILABLE: "Not available",
  BLOCKED: "Blocked",
};

export function NatureBadge({ nature }: { nature: string }) {
  const tone: BadgeTone = nature === "REAL" ? "neutral" : nature === "BLOCKED" ? "info" : "gold";
  return <Badge tone={tone}>{NATURE_LABEL[nature] ?? humanise(nature)}</Badge>;
}

export function HealthBadge({ status }: { status: string | null | undefined }) {
  if (!status) return null;
  const tone: BadgeTone = status === "HEALTHY" ? "brand" : status === "DEGRADED" || status === "UNAVAILABLE" ? "gold" : "info";
  return <Badge tone={tone}>{humanise(status)}</Badge>;
}

export function SevBadge({ severity }: { severity: string }) {
  return <Badge tone={severity === "SEV1" || severity === "SEV2" || severity === "P1" || severity === "P2" ? "gold" : "neutral"}>{severity}</Badge>;
}

export function ShortId({ value }: { value: string | null | undefined }) {
  if (!value) return <span>—</span>;
  return (
    <span title={value}>
      <Code>{value.slice(0, 8)}</Code>
    </span>
  );
}

/** The fixed TEST-environment notice on every operations page. */
export function OpsBanner({ notice }: { notice?: string }) {
  return (
    <Callout tone="notice" title="TEST environment">
      {notice ||
        "Operational counts from each module's own records. Payments, ledger, fees and payouts are TEST or SIMULATED data and never business performance. Live payments, real payouts and production screening are disabled."}
    </Callout>
  );
}

/** A module that could not be read is shown as unavailable — its numbers are missing, never zero. */
export function SourcesNotice({ sources }: { sources: SourceStatus[] }) {
  const down = sources.filter((s) => s.status !== "HEALTHY");
  if (down.length === 0) return null;
  return (
    <Callout tone="danger" title="Some modules could not be read">
      <p>Their figures are missing from this page (not zero). Reload to try again.</p>
      <ul className="mt-2 list-disc pl-6">
        {down.map((s) => (
          <li key={s.module}>
            {humanise(s.module)}: {humanise(s.status)}
          </li>
        ))}
      </ul>
    </Callout>
  );
}

function metricValue(m: Metric): ReactNode {
  if (m.value === null) return <span className="text-ink-600">Not measured</span>;
  return <span className="font-display text-3xl font-semibold text-ink-900">{m.value.toLocaleString("en-GB")}</span>;
}

/** Metrics grouped by module; each card names its source. */
export function MetricsGrid({ metrics, empty = "No metrics you can view." }: { metrics: Metric[]; empty?: string }) {
  if (metrics.length === 0) return <p className="text-ink-700">{empty}</p>;
  const groups = new Map<string, Metric[]>();
  for (const m of metrics) groups.set(m.module, [...(groups.get(m.module) ?? []), m]);
  return (
    <div className="space-y-6">
      {[...groups.entries()].map(([module, ms]) => (
        <section key={module} aria-labelledby={`metrics-${module}`} className="space-y-3">
          <h3 id={`metrics-${module}`} className="font-display text-xl font-semibold text-ink-900">
            {humanise(module)}
          </h3>
          <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {ms.map((m) => (
              <li key={m.key} className="space-y-2 rounded-card border border-line bg-surface p-4">
                <p className="font-semibold text-ink-900">{m.href ? <Link href={m.href} className="underline underline-offset-4">{m.label}</Link> : m.label}</p>
                <p>{metricValue(m)}</p>
                <p className="flex flex-wrap gap-2">
                  <NatureBadge nature={m.nature} />
                  <HealthBadge status={m.status} />
                </p>
                <p className="text-sm text-ink-600">Source: {m.source}</p>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

export function AlertsList({ alerts, canDeclare, declare }: { alerts: Alert[]; canDeclare?: boolean; declare?: (a: Alert) => ReactNode }) {
  if (alerts.length === 0) return <p className="text-ink-700">No alerts. Alerts are evaluated from recorded evidence when the page loads.</p>;
  return (
    <TableShell caption="Alerts" head={["Severity", "Alert", "Source", "Observed", "Incident"]}>
      {alerts.map((a) => (
        <tr key={a.key} className={row}>
          <td className={cell}>
            <SevBadge severity={a.severity} />
          </td>
          <td className={cell}>
            {a.title}
            <div>
              <Code>{a.key}</Code>
            </div>
          </td>
          <td className={cell}>{a.source}</td>
          <td className={cell}>
            <When at={a.observed_at} />
          </td>
          <td className={cell}>
            {a.incident_id ? (
              <Link href={`/admin/operations/incidents/${encodeURIComponent(a.incident_id)}`} className="underline underline-offset-4">
                Open incident
              </Link>
            ) : canDeclare && declare ? (
              declare(a)
            ) : (
              "None"
            )}
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

const BLOCKED_COPY: Record<string, string> = {
  MAKER: "You made or requested this — a different person must decide.",
  PERMISSION: "You do not hold the permission to decide this.",
};

export function ApprovalsTable({ items, caption }: { items: Approval[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">Nothing is waiting for a decision you can see.</p>;
  return (
    <TableShell caption={caption} head={["Waiting", "Module", "Decision", "Requested by", "Required permission", "Next step"]} minWidthClass="min-w-[56rem]">
      {items.map((a) => (
        <tr key={a.id} className={row}>
          <td className={cell}>{formatDuration(a.age_seconds)}</td>
          <td className={cell}>{humanise(a.source_module)}</td>
          <td className={cell}>
            {a.summary}
            <div className="text-sm text-ink-600">{humanise(a.action)}</div>
          </td>
          <td className={cell}>
            <ShortId value={a.requested_by} />
          </td>
          <td className={cell}>
            <Code>{a.required_permission}</Code>
          </td>
          <td className={cell}>
            {a.can_decide ? (
              <Link href={a.href} className="font-semibold underline underline-offset-4">
                Review and decide in {humanise(a.source_module)}
              </Link>
            ) : (
              <>
                <span>{BLOCKED_COPY[a.blocked_reason ?? ""] ?? "You cannot decide this."}</span>
                <div>
                  <Link href={a.href} className="underline underline-offset-4">
                    View
                  </Link>
                </div>
              </>
            )}
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export const exceptionHref = (e: { source_module: string; id: string }) =>
  `/admin/operations/exceptions/${encodeURIComponent(e.source_module)}/${encodeURIComponent(e.id)}`;

export function ExceptionsTable({ items, caption, canWork }: { items: ExceptionItem[]; caption: string; canWork: boolean }) {
  if (items.length === 0) return <p className="text-ink-700">No open exceptions you can see.</p>;
  return (
    <TableShell caption={caption} head={["Severity", "Module", "Exception", "Since", "Domain status", "Triage", "Open"]} minWidthClass="min-w-[60rem]">
      {items.map((e) => (
        <tr key={e.id} className={row}>
          <td className={cell}>
            <SevBadge severity={e.severity} />
          </td>
          <td className={cell}>{humanise(e.source_module)}</td>
          <td className={cell}>
            {e.summary}
            <div className="text-sm text-ink-600">{humanise(e.category)}</div>
          </td>
          <td className={cell}>
            <When at={e.created_at} />
          </td>
          <td className={cell}>{e.domain_open ? humanise(e.domain_status) : "Closed in the module"}</td>
          <td className={cell}>
            {humanise(e.triage_status)}
            {e.assigned_to ? (
              <div className="text-sm">
                Assigned <ShortId value={e.assigned_to} />
              </div>
            ) : null}
          </td>
          <td className={cell}>
            <div className="flex flex-col gap-1">
              <Link href={e.href} className="underline underline-offset-4">
                In {humanise(e.source_module)}
              </Link>
              {canWork ? (
                <Link href={exceptionHref(e)} className="underline underline-offset-4">
                  Triage
                </Link>
              ) : null}
            </div>
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function HistoryList({ items, caption }: { items: HistoryEntry[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No history yet.</p>;
  return (
    <TableShell caption={caption} head={["When", "Event", "By", "Change", "Note"]}>
      {items.map((h, i) => (
        <tr key={`${h.occurred_at}-${i}`} className={row}>
          <td className={cell}>
            <When at={h.occurred_at} />
          </td>
          <td className={cell}>{humanise(h.event_type)}</td>
          <td className={cell}>
            <ShortId value={h.actor_id} />
          </td>
          <td className={cell}>{h.to_status ? `${h.from_status ? humanise(h.from_status) : "—"} → ${humanise(h.to_status)}` : "—"}</td>
          <td className={`${cell} whitespace-pre-wrap break-words`}>{h.note ?? "—"}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export const supportHref = (id: string) => `/admin/support/${encodeURIComponent(id)}`;

export function SupportTable({ items, caption }: { items: SupportCase[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No support cases match.</p>;
  return (
    <TableShell caption={caption} head={["Case", "Subject", "Category", "Priority", "Status", "Assigned", "Due"]} minWidthClass="min-w-[56rem]">
      {items.map((c) => (
        <tr key={c.id} className={row}>
          <td className={cell}>
            <Link href={supportHref(c.id)} className="font-semibold underline underline-offset-4">
              #{c.case_number}
            </Link>
          </td>
          <td className={`${cell} break-words`}>{c.subject}</td>
          <td className={cell}>{humanise(c.category)}</td>
          <td className={cell}>
            <SevBadge severity={c.priority} />
          </td>
          <td className={cell}>
            {humanise(c.status)}
            {c.escalated_to ? <div className="text-sm">Escalated to {humanise(c.escalated_to)}</div> : null}
          </td>
          <td className={cell}>{c.assigned_to ? <ShortId value={c.assigned_to} /> : "Unassigned"}</td>
          <td className={cell}>{c.due_at ? <When at={c.due_at} /> : "No internal target"}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export const incidentHref = (id: string) => `/admin/operations/incidents/${encodeURIComponent(id)}`;

export function IncidentsTable({ items, caption }: { items: Incident[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No incidents match.</p>;
  return (
    <TableShell caption={caption} head={["Incident", "Title", "Severity", "Module", "Status", "Owner", "Started"]} minWidthClass="min-w-[56rem]">
      {items.map((x) => (
        <tr key={x.id} className={row}>
          <td className={cell}>
            <Link href={incidentHref(x.id)} className="font-semibold underline underline-offset-4">
              INC-{x.incident_number}
            </Link>
          </td>
          <td className={`${cell} break-words`}>{x.title}</td>
          <td className={cell}>
            <SevBadge severity={x.severity} />
          </td>
          <td className={cell}>{humanise(x.affected_module)}</td>
          <td className={cell}>{humanise(x.status)}</td>
          <td className={cell}>{x.owner_id ? <ShortId value={x.owner_id} /> : "No owner"}</td>
          <td className={cell}>
            <When at={x.started_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export const staffHref = (id: string) => `/admin/staff/${encodeURIComponent(id)}`;

export function StaffTable({ items }: { items: StaffMember[] }) {
  if (items.length === 0) return <p className="text-ink-700">No staff accounts match.</p>;
  return (
    <TableShell caption="Staff accounts" head={["Name", "Status", "Roles", "MFA", "Active sessions", "Last activity"]} minWidthClass="min-w-[52rem]">
      {items.map((m) => (
        <tr key={m.id} className={row}>
          <td className={cell}>
            <Link href={staffHref(m.id)} className="font-semibold underline underline-offset-4">
              {m.display_name}
            </Link>
          </td>
          <td className={cell}>{humanise(m.status)}</td>
          <td className={cell}>{m.roles.length ? m.roles.map(humanise).join(", ") : "No role"}</td>
          <td className={cell}>{m.mfa_confirmed ? "Confirmed" : "Not confirmed"}</td>
          <td className={cell}>{m.active_sessions}</td>
          <td className={cell}>{m.last_activity_at ? <When at={m.last_activity_at} /> : "Never"}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export function AuditTable({ items, caption }: { items: AuditRecord[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No audit records match.</p>;
  return (
    <TableShell caption={caption} head={["#", "When", "Action", "Actor", "Target", "Outcome", "Correlation"]} minWidthClass="min-w-[60rem]">
      {items.map((e) => (
        <tr key={e.id} className={row}>
          <td className={cell}>{e.seq}</td>
          <td className={cell}>
            <When at={e.occurred_at} />
          </td>
          <td className={cell}>
            <Code>{e.action}</Code>
            {e.break_glass ? <div className="text-sm font-semibold">Break-glass</div> : null}
            {e.reason ? <div className="text-sm text-ink-600 break-words">{e.reason}</div> : null}
          </td>
          <td className={cell}>
            {humanise(e.actor_type)} <ShortId value={e.actor_id} />
            {e.actor_role ? <div className="text-sm">{humanise(e.actor_role)}</div> : null}
          </td>
          <td className={cell}>
            {e.target_type} <ShortId value={e.target_id} />
          </td>
          <td className={cell}>{humanise(e.outcome)}</td>
          <td className={cell}>
            {e.correlation_id ? (
              <Link href={`/admin/audit?correlation_id=${encodeURIComponent(e.correlation_id)}`} className="underline underline-offset-4">
                <ShortId value={e.correlation_id} />
              </Link>
            ) : (
              "—"
            )}
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

/** "You cannot do this here" panel for an action the viewer's role does not include (the API refuses it anyway). */
export function NoPermission({ action, permission }: { action: string; permission: string }) {
  return (
    <p className="rounded-card border border-line bg-surface-muted p-4 text-ink-700">
      {action} needs the <Code>{permission}</Code> permission, which your roles do not include.
    </p>
  );
}

export { Section };
