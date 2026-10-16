import Link from "next/link";
import type { ReactNode } from "react";

import { Code, Facts, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { humanise } from "@/lib/payments/labels";
import { classificationTone, durationText, outcomeLabel, screeningStatusLabel, short } from "@/lib/risk/labels";
import type {
  Count,
  Evaluation,
  Evidence,
  Factor,
  HistoryEvent,
  Hold,
  MonitoringEvent,
  RiskCase,
  RiskSignal,
  RuleOutcome,
  RuleSummary,
  ScreeningRequest,
} from "@/lib/risk/types";

/**
 * Shared, server-renderable pieces of the staff risk operations pages (Stage 13, ADR-045). Every value from the API is
 * rendered as escaped text. Risk data is staff-only and TEST: a score is a prioritisation aid, never a finding.
 */
export const RISK_BASE = "/admin/risk";
export const ruleHref = (id: string) => `${RISK_BASE}/rules/${encodeURIComponent(id)}`;
export const caseHref = (id: string) => `${RISK_BASE}/cases/${encodeURIComponent(id)}`;
export const evaluationHref = (id: string) => `${RISK_BASE}/evaluations/${encodeURIComponent(id)}`;
export const screeningHref = (id: string) => `${RISK_BASE}/screening/${encodeURIComponent(id)}`;

const row = "border-b border-line align-top last:border-b-0";
const cell = "p-3";

/** Every risk page: TEST data, production screening BLOCKED, accuracy NOT_MEASURED, a score is never proof. */
export function RiskBanner() {
  return (
    <div className="rounded-card border-2 border-gold-500/50 bg-gold-100 p-4">
      <p className="text-lg font-semibold text-ink-900">
        TEST DATA <Badge tone="gold">TEST</Badge>
      </p>
      <p className="text-ink-800">
        TEST data · production screening BLOCKED · accuracy NOT_MEASURED · a risk score is a prioritisation aid, never proof of fraud.
      </p>
    </div>
  );
}

export function TestOnlyBadge() {
  return (
    <Badge tone="gold" className="uppercase">
      TEST_ONLY
    </Badge>
  );
}

export function ClassBadge({ classification }: { classification: string | null }) {
  if (!classification) return <span>—</span>;
  return <Badge tone={classificationTone(classification)}>{humanise(classification)}</Badge>;
}

export function Id({ value }: { value: string | null | undefined }) {
  if (!value) return <span>—</span>;
  return <Code>{value}</Code>;
}

export function CountsFacts({ items, empty = "None" }: { items: Count[]; empty?: string }) {
  if (items.length === 0) return <p className="text-ink-700">{empty}</p>;
  return <Facts items={items.map((c) => [humanise(c.key), String(c.count)] as [string, ReactNode])} />;
}

export function SignalsTable({ items, caption }: { items: RiskSignal[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No signals match" description="Change the filters or check again later." />;
  return (
    <TableShell caption={caption} head={["Signal", "Subject", "Severity / confidence", "Facts", "Reviews", "Observed"]} minWidthClass="min-w-[60rem]">
      {items.map((s) => (
        <tr key={s.id} className={row}>
          <th scope="row" className={`${cell} font-semibold`}>
            {humanise(s.signal_type)}
            <span className="block text-sm font-normal text-ink-600">
              {humanise(s.category)} · {humanise(s.kind)} · from {s.source_module}
            </span>
          </th>
          <td className={`${cell} text-sm`}>
            {humanise(s.subject_type)} <Id value={s.subject_id} />
          </td>
          <td className={cell}>
            {humanise(s.severity)} / {s.confidence}
          </td>
          <td className={`${cell} text-sm`}>{Object.keys(s.facts).length ? Object.entries(s.facts).map(([k, v]) => `${k}: ${v}`).join(", ") : "—"}</td>
          <td className={`${cell} text-sm`}>{s.review_classifications.length ? s.review_classifications.map(humanise).join(", ") : "Not reviewed"}</td>
          <td className={`${cell} text-sm`}>
            <When at={s.observed_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function EvaluationsTable({ items, caption }: { items: Evaluation[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No evaluations match" description="Evaluations appear when an active rule covers a signal's subject." />;
  return (
    <TableShell caption={caption} head={["Evaluation", "Subject", "Score", "Classification", "Recommended action", "Evaluated"]} minWidthClass="min-w-[56rem]">
      {items.map((e) => (
        <tr key={e.id} className={row}>
          <th scope="row" className={`${cell} font-normal`}>
            <Link href={evaluationHref(e.id)} className="text-brand-700 underline">
              {short(e.id)}
            </Link>
            <span className="block text-sm text-ink-600">{e.engine_version}</span>
          </th>
          <td className={`${cell} text-sm`}>
            {humanise(e.subject_type)} <Id value={e.subject_id} />
          </td>
          <td className={cell}>{e.score} / 100</td>
          <td className={cell}>
            <ClassBadge classification={e.classification} />
          </td>
          <td className={cell}>{humanise(e.recommended_action)}</td>
          <td className={`${cell} text-sm`}>
            <When at={e.evaluated_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

/** The plain-language explanation of a score: fired rules and their contributions. */
export function Explanation({ factors }: { factors: Factor[] }) {
  if (factors.length === 0) return <p className="text-ink-700">No rule fired: score 0.</p>;
  return (
    <ul className="list-disc space-y-1 pl-5 text-ink-800">
      {factors.map((f) => (
        <li key={f.rule_code}>
          <strong>{f.rule_name}</strong> ({f.rule_code}): +{f.contribution}
          {f.summary ? <span className="block text-sm text-ink-600">{f.summary}</span> : null}
        </li>
      ))}
    </ul>
  );
}

export function OutcomesTable({ items, caption }: { items: RuleOutcome[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No rule results.</p>;
  return (
    <TableShell caption={caption} head={["Rule", "Fired", "Contribution", "Action", "Conditions", "Matched signals"]} minWidthClass="min-w-[56rem]">
      {items.map((o) => (
        <tr key={o.rule_version_id + o.rule_code} className={row}>
          <th scope="row" className={`${cell} font-semibold`}>
            {o.rule_code}
          </th>
          <td className={cell}>{o.fired ? "Yes" : "No"}</td>
          <td className={cell}>{o.contribution}</td>
          <td className={cell}>{humanise(o.action)}</td>
          <td className={`${cell} text-sm`}>{o.conditions.join("; ") || "—"}</td>
          <td className={`${cell} text-sm`}>{o.signal_ids.length}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export function RulesTable({ items, caption }: { items: RuleSummary[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No risk rules yet" description="No rule is seeded: nothing is active until two people approve it." />;
  return (
    <TableShell caption={caption} head={["Rule", "Subject", "Category", "In force", "Pending"]} minWidthClass="min-w-[48rem]">
      {items.map((r) => (
        <tr key={r.id} className={row}>
          <th scope="row" className={`${cell} font-normal`}>
            <Link href={ruleHref(r.id)} className="text-brand-700 underline">
              {r.name}
            </Link>
            <span className="block text-sm text-ink-600">{r.code}</span>
          </th>
          <td className={cell}>{humanise(r.subject_type)}</td>
          <td className={cell}>{humanise(r.category)}</td>
          <td className={cell}>{r.active_version_no ? `v${r.active_version_no} (${humanise(r.active_action ?? "")})` : "None"}</td>
          <td className={cell}>{r.pending_version_no ? `v${r.pending_version_no}` : "—"}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export function CasesTable({ items, caption }: { items: RiskCase[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No risk cases match" description="Change the filters or check again later." />;
  return (
    <TableShell caption={caption} head={["Case", "Type", "Subject", "Status", "Priority", "Classification", "Due"]} minWidthClass="min-w-[60rem]">
      {items.map((c) => (
        <tr key={c.id} className={row}>
          <th scope="row" className={`${cell} font-normal`}>
            <Link href={caseHref(c.id)} className="text-brand-700 underline">
              Case {c.case_number}
            </Link>
            <span className="block text-sm text-ink-600">{c.assigned_to ? `Assigned ${short(c.assigned_to)}` : "Unassigned"}</span>
          </th>
          <td className={cell}>{humanise(c.case_type)}</td>
          <td className={`${cell} text-sm`}>
            {humanise(c.subject_type)} <Id value={c.subject_id} />
          </td>
          <td className={cell}>
            <StatusBadge status={c.status} />
          </td>
          <td className={cell}>{c.priority}</td>
          <td className={cell}>
            <ClassBadge classification={c.classification} />
          </td>
          <td className={`${cell} text-sm`}>
            <When at={c.due_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function caseFacts(c: RiskCase): Array<[string, ReactNode]> {
  return [
    ["Type", humanise(c.case_type)],
    ["Subject", <span key="s">{humanise(c.subject_type)} <Id value={c.subject_id} /></span>],
    ["Status", <StatusBadge key="st" status={c.status} />],
    ["Priority", c.priority],
    ["Score", c.risk_score === null ? "—" : `${c.risk_score} / 100`],
    ["Classification", <ClassBadge key="c" classification={c.classification} />],
    ["Team", humanise(c.assigned_team)],
    ["Assigned to", c.assigned_to ? <Id key="a" value={c.assigned_to} /> : "Unassigned"],
    ["Due (internal target, not a regulatory deadline)", <When key="d" at={c.due_at} />],
    ["Opened by", c.opened_by_type === "SYSTEM" ? "Risk engine" : <Id key="o" value={c.opened_by} />],
    ["Proposed outcome", c.proposed_outcome ? `${outcomeLabel(c.proposed_outcome)}: ${c.proposed_reason ?? ""}` : "—"],
    ["Decision", c.decision_outcome ? `${outcomeLabel(c.decision_outcome)}: ${c.decision_reason ?? ""}` : "—"],
  ];
}

export function HoldsTable({ items, caption, actions }: { items: Hold[]; caption: string; actions?: (h: Hold) => ReactNode }) {
  if (items.length === 0) return <EmptyState title="No holds match" description="Holds stop one kind of action on one subject until released by two people." />;
  return (
    <TableShell caption={caption} head={["Hold", "Subject", "Status", "Reason", "Expires", "Actions"]} minWidthClass="min-w-[64rem]">
      {items.map((h) => (
        <tr key={h.id} className={row}>
          <th scope="row" className={`${cell} font-semibold`}>
            {humanise(h.hold_type)}
            <span className="block text-sm font-normal text-ink-600">
              {h.automated ? "Automated (temporary)" : "Placed by staff"} · <Code>{short(h.id)}</Code>
            </span>
          </th>
          <td className={`${cell} text-sm`}>
            {humanise(h.subject_type)} <Id value={h.subject_id} />
          </td>
          <td className={cell}>
            <StatusBadge status={h.status} />
          </td>
          <td className={`${cell} text-sm`}>
            {h.reason_code}
            {h.description ? <span className="block text-ink-600">{h.description}</span> : null}
            {h.release_reason ? <span className="block text-ink-600">Release requested: {h.release_reason}</span> : null}
          </td>
          <td className={`${cell} text-sm`}>{h.expires_at ? <When at={h.expires_at} /> : durationText(null)}</td>
          <td className={cell}>
            <div className="flex flex-wrap gap-2">{actions ? actions(h) : null}</div>
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function ScreeningsTable({ items, caption }: { items: ScreeningRequest[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No screening requests" description="Request a TEST screening with a simulator scenario." />;
  return (
    <TableShell caption={caption} head={["Request", "Subject", "Status", "Matches", "Requested"]} minWidthClass="min-w-[56rem]">
      {items.map((s) => (
        <tr key={s.id} className={row}>
          <th scope="row" className={`${cell} font-normal`}>
            <Link href={screeningHref(s.id)} className="text-brand-700 underline">
              {s.synthetic_reference}
            </Link>{" "}
            <TestOnlyBadge />
          </th>
          <td className={`${cell} text-sm`}>
            {humanise(s.subject_type)} <Id value={s.subject_id} />
          </td>
          <td className={cell}>{screeningStatusLabel(s.status)}</td>
          <td className={cell}>{s.match_count}</td>
          <td className={`${cell} text-sm`}>
            <When at={s.created_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function MonitoringTable({ items, caption }: { items: MonitoringEvent[]; caption: string }) {
  if (items.length === 0) return <EmptyState title="No monitoring events" description="Alerts, expiries, rejected callbacks and reconciliation findings appear here." />;
  return (
    <TableShell caption={caption} head={["Event", "Severity", "Subject", "Detail", "When"]} minWidthClass="min-w-[56rem]">
      {items.map((m) => (
        <tr key={m.id} className={row}>
          <th scope="row" className={`${cell} font-semibold`}>
            {humanise(m.event_type)}
          </th>
          <td className={cell}>{humanise(m.severity)}</td>
          <td className={`${cell} text-sm`}>{m.subject_type ? <>{humanise(m.subject_type)} <Id value={m.subject_id} /></> : "—"}</td>
          <td className={`${cell} text-sm`}>{Object.entries(m.detail).map(([k, v]) => `${k}: ${v}`).join(", ") || "—"}</td>
          <td className={`${cell} text-sm`}>
            <When at={m.occurred_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function HistoryTable({ items, caption }: { items: HistoryEvent[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No history yet.</p>;
  return (
    <TableShell caption={caption} head={["Event", "Actor", "Detail", "When"]} minWidthClass="min-w-[48rem]">
      {items.map((e, i) => (
        <tr key={`${e.event_type}-${i}`} className={row}>
          <th scope="row" className={`${cell} font-semibold`}>
            {humanise(e.event_type)}
          </th>
          <td className={`${cell} text-sm`}>{e.actor_type === "SYSTEM" ? e.actor_id : <Id value={e.actor_id} />}</td>
          <td className={`${cell} text-sm`}>{Object.entries(e.detail).filter(([, v]) => v !== "").map(([k, v]) => `${k}: ${v}`).join(", ") || "—"}</td>
          <td className={`${cell} text-sm`}>
            <When at={e.occurred_at} />
          </td>
        </tr>
      ))}
    </TableShell>
  );
}

export function EvidenceTable({ items, caption, actions }: { items: Evidence[]; caption: string; actions?: (e: Evidence) => ReactNode }) {
  if (items.length === 0) return <p className="text-ink-700">No evidence yet.</p>;
  return (
    <TableShell caption={caption} head={["Evidence", "Content", "Integrity", "Added", "Actions"]} minWidthClass="min-w-[64rem]">
      {items.map((e) => (
        <tr key={e.id} className={row}>
          <th scope="row" className={`${cell} font-semibold`}>
            {humanise(e.evidence_type)}
            <span className="block text-sm font-normal text-ink-600">
              {e.reference_type ? `${e.reference_type} ` : ""}
              {e.reference_id ? <Code>{e.reference_id}</Code> : null}
            </span>
            {e.superseded_by ? <Badge tone="info">Superseded</Badge> : null}
            {e.supersedes_id ? <span className="block text-sm font-normal text-ink-600">Corrects {short(e.supersedes_id)}</span> : null}
          </th>
          <td className={`${cell} text-sm`}>{Object.entries(e.content).map(([k, v]) => `${k}: ${v}`).join(", ") || "—"}</td>
          <td className={`${cell} text-sm`}>
            SHA-256 <Code>{e.content_hash || "unavailable"}</Code>
            <span className="block text-ink-600">Retention: {humanise(e.retention_class)}</span>
          </td>
          <td className={`${cell} text-sm`}>
            {e.added_by_type === "SYSTEM" ? e.added_by : <Id value={e.added_by} />}
            <span className="block">
              <When at={e.added_at} />
            </span>
          </td>
          <td className={cell}>{actions ? actions(e) : null}</td>
        </tr>
      ))}
    </TableShell>
  );
}
