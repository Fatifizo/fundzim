import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { TableShell, When } from "@/components/admin/payments/common";
import { NoPermission, OpsBanner, Section, ShortId } from "@/components/admin/operations/ops";
import { StartAccessReviewForm } from "@/components/admin/operations/ops-forms";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items } from "@/lib/operations/normalise";
import type { AccessReview, RoleInventoryItem } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Access reviews", robots: { index: false, follow: false, nocache: true } };

/**
 * Staff access reviews (`staff.view`; starting needs `access.review.run`, deciding `access.review.certify`). No review
 * frequency is invented: reviews are started by staff when needed.
 */
export default async function AccessReviewsPage() {
  const me = await requireStaff();
  const [list, roles] = await Promise.all([
    serverGetOr404<unknown>("/api/v1/admin/access-reviews", "/admin/staff/access-reviews").then((r) => items<AccessReview>(r.data)),
    serverGetOptional<unknown>("/api/v1/admin/staff/roles", "/admin/staff/access-reviews", null).then((d) => items<RoleInventoryItem>(d).map((r) => r.code)),
  ]);
  return (
    <AccountPage title="Access reviews" intro="Each active role assignment is certified or recommended for revocation by a named reviewer. Revocations are dual-controlled role requests.">
      <OpsBanner />
      <Section id="list" title="Reviews">
        {list.length === 0 ? (
          <p className="text-ink-700">No access reviews yet.</p>
        ) : (
          <TableShell caption="Access reviews" head={["Review", "Scope", "Status", "Reviewer", "Pending items", "Started"]}>
            {list.map((r) => (
              <tr key={r.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3">
                  <Link href={`/admin/staff/access-reviews/${encodeURIComponent(r.id)}`} className="font-semibold underline underline-offset-4">
                    {r.title}
                  </Link>
                </td>
                <td className="p-3">{r.scope_role ? humanise(r.scope_role) : "Every role"}</td>
                <td className="p-3">{humanise(r.status)}</td>
                <td className="p-3">{r.reviewer_id === me.id ? "You" : <ShortId value={r.reviewer_id} />}</td>
                <td className="p-3">{r.pending_items}</td>
                <td className="p-3">
                  <When at={r.created_at} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="start" title="Start a review">
        {can(me.permissions, "access.review.run") ? <StartAccessReviewForm roles={roles} /> : <NoPermission action="Starting an access review" permission="access.review.run" />}
      </Section>
    </AccountPage>
  );
}
