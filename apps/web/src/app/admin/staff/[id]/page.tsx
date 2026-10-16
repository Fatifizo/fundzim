import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Code, Facts, TableShell, When } from "@/components/admin/payments/common";
import { OpsBanner, Section, ShortId } from "@/components/admin/operations/ops";
import { RevokeSessions, RoleRequestDecision } from "@/components/admin/operations/ops-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { obj } from "@/lib/operations/normalise";
import type { StaffDetail } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Staff member", robots: { index: false, follow: false, nocache: true } };

/**
 * One staff member (`staff.view`, audited): roles, effective permissions, role history, pending role requests and
 * recent security event types. Ending sessions needs `session.revoke`; deciding role requests `role.assign.approve`.
 */
export default async function StaffMemberPage({ params }: PageProps<"/admin/staff/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  const d = obj((await serverGetOr404<unknown>(`/api/v1/admin/staff/${encodeURIComponent(id)}`, `/admin/staff/${encodeURIComponent(id)}`)).data) as unknown as StaffDetail;
  const m = d.member;
  const self = m.id === me.id;
  const decide = can(me.permissions, "role.assign.approve");
  return (
    <AccountPage title={m.display_name} intro={self ? "This is your own staff account." : undefined}>
      <OpsBanner />
      <Section id="facts" title="Account">
        <Facts
          items={[
            ["Status", humanise(m.status)],
            ["Roles", m.roles.length ? m.roles.map(humanise).join(", ") : "No role"],
            ["MFA", m.mfa_confirmed ? "Confirmed" : "Not confirmed"],
            ["Active sessions", String(m.active_sessions)],
            ["Last activity", m.last_activity_at ? <When key="l" at={m.last_activity_at} /> : "Never"],
            ["Personal account linked", m.personal_account_linked ? "Yes (separation of duties applies to both)" : "No"],
          ]}
        />
      </Section>
      {!self && can(me.permissions, "session.revoke") ? (
        <Section id="sessions" title="Sessions">
          <RevokeSessions staffId={m.id} name={m.display_name} />
        </Section>
      ) : null}
      <Section id="pending" title="Pending role requests" description="A role is granted or removed only when a second person approves the request.">
        {(d.pending_role_requests ?? []).length === 0 ? (
          <p className="text-ink-700">None.</p>
        ) : (
          <ul className="space-y-3">
            {d.pending_role_requests.map((r) => (
              <li key={r.id} className="space-y-2 rounded-card border border-line bg-surface p-4">
                <p className="font-semibold">
                  {humanise(r.action)} {humanise(r.role_code)}
                </p>
                <p className="text-sm text-ink-700">
                  Requested by <ShortId value={r.requested_by} /> on <When at={r.created_at} />. Expires <When at={r.expires_at} />.
                </p>
                <p className="break-words text-ink-700">{r.justification}</p>
                {decide && !self && r.requested_by !== me.id ? (
                  <RoleRequestDecision requestId={r.id} action={r.action} role={r.role_code} />
                ) : (
                  <p className="text-sm text-ink-600">{self || r.requested_by === me.id ? "You cannot decide this request." : "Deciding needs the role approval permission."}</p>
                )}
              </li>
            ))}
          </ul>
        )}
      </Section>
      <Section id="permissions" title={`Effective permissions (${(d.permissions ?? []).length})`}>
        <ul className="flex flex-wrap gap-2">
          {(d.permissions ?? []).map((p) => (
            <li key={p}>
              <Code>{p}</Code>
            </li>
          ))}
        </ul>
      </Section>
      <Section id="history" title="Role history">
        <TableShell caption="Role history" head={["Role", "From", "Granted by", "Approved by", "Ended", "Reason"]}>
          {(d.role_history ?? []).map((h, i) => (
            <tr key={`${h.role}-${i}`} className="border-b border-line align-top last:border-b-0">
              <td className="p-3">{humanise(h.role)}</td>
              <td className="p-3">
                <When at={h.valid_from} />
              </td>
              <td className="p-3">
                <ShortId value={h.granted_by} />
              </td>
              <td className="p-3">
                <ShortId value={h.approved_by} />
              </td>
              <td className="p-3">{h.revoked_at ? <When at={h.revoked_at} /> : h.expires_at ? <>Expires <When at={h.expires_at} /></> : "Active"}</td>
              <td className="p-3 break-words">{h.revoke_reason ?? "—"}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="security" title="Recent security events">
        {(d.recent_security_events ?? []).length === 0 ? (
          <p className="text-ink-700">None recorded.</p>
        ) : (
          <ul className="space-y-1">
            {d.recent_security_events.map((e, i) => (
              <li key={`${e.type}-${i}`}>
                {humanise(e.type)} — <When at={e.occurred_at} />
              </li>
            ))}
          </ul>
        )}
      </Section>
    </AccountPage>
  );
}
