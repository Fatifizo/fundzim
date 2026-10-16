import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, TableShell, When } from "@/components/admin/payments/common";
import { OpsBanner, Section, ShortId, staffHref } from "@/components/admin/operations/ops";
import { CompleteReview, ReviewItemDecision } from "@/components/admin/operations/ops-actions";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items, obj } from "@/lib/operations/normalise";
import type { AccessReview, StaffMember } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Access review", robots: { index: false, follow: false, nocache: true } };

/** One access review: the reviewer decides each item (never their own access); the database refuses changing a decision. */
export default async function AccessReviewPage({ params }: PageProps<"/admin/staff/access-reviews/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  const here = `/admin/staff/access-reviews/${encodeURIComponent(id)}`;
  const [rv, staff] = await Promise.all([
    serverGetOr404<unknown>(`/api/v1/admin/access-reviews/${encodeURIComponent(id)}`, here).then((r) => obj(r.data) as unknown as AccessReview),
    serverGetOptional<unknown>("/api/v1/admin/staff", here, null).then((d) => new Map(items<StaffMember>(d).map((m) => [m.id, m.display_name]))),
  ]);
  const name = (sid: string) => staff.get(sid) ?? `Staff ${sid.slice(0, 8)}`;
  const reviewer = rv.reviewer_id === me.id && can(me.permissions, "access.review.certify") && rv.status === "OPEN";
  return (
    <AccountPage title={rv.title}>
      <OpsBanner />
      <Section id="facts" title="Review">
        <Facts
          items={[
            ["Status", humanise(rv.status)],
            ["Scope", rv.scope_role ? humanise(rv.scope_role) : "Every role"],
            ["Reviewer", rv.reviewer_id === me.id ? "You" : name(rv.reviewer_id)],
            ["Started by", rv.created_by === me.id ? "You" : name(rv.created_by)],
            ["Pending items", String(rv.pending_items)],
            ["Started", <When key="s" at={rv.created_at} />],
          ]}
        />
      </Section>
      <Section id="items" title="Role assignments">
        <TableShell caption="Access review items" head={["Staff member", "Role", "Decision", "Reason", "Revocation request", ""]} minWidthClass="min-w-[56rem]">
          {(rv.items ?? []).map((it) => (
            <tr key={it.id} className="border-b border-line align-top last:border-b-0">
              <td className="p-3">
                <Link href={staffHref(it.staff_user_id)} className="underline underline-offset-4">
                  {name(it.staff_user_id)}
                </Link>
              </td>
              <td className="p-3">{humanise(it.role_code)}</td>
              <td className="p-3">
                {humanise(it.decision)}
                {it.decided_at ? (
                  <div className="text-sm">
                    <When at={it.decided_at} />
                  </div>
                ) : null}
              </td>
              <td className="p-3 break-words">{it.reason ?? "—"}</td>
              <td className="p-3">{it.revoke_request_id ? <ShortId value={it.revoke_request_id} /> : "—"}</td>
              <td className="p-3">
                {reviewer && it.decision === "PENDING" ? (
                  it.staff_user_id === me.id ? (
                    "Your own access: another reviewer must decide"
                  ) : (
                    <ReviewItemDecision reviewId={rv.id} itemId={it.id} staffName={name(it.staff_user_id)} role={it.role_code} />
                  )
                ) : null}
              </td>
            </tr>
          ))}
        </TableShell>
      </Section>
      {rv.status === "OPEN" && can(me.permissions, "access.review.run") ? (
        <Section id="complete" title="Complete">
          <CompleteReview reviewId={rv.id} />
        </Section>
      ) : null}
    </AccountPage>
  );
}
