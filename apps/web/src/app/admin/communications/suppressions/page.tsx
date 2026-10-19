import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Section, TableShell, When } from "@/components/admin/payments/common";
import { NoPermission, ShortId } from "@/components/admin/operations/ops";
import { CommsBanner } from "@/components/admin/communications/comms";
import { LiftSuppression, SuppressForm } from "@/components/admin/communications/comms-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items } from "@/lib/operations/normalise";
import type { Suppression } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Suppressions", robots: { index: false, follow: false, nocache: true } };

/** Destination suppressions (`communications.view`; changes need `communications.suppression.manage`). Destinations are stored as hashes. */
export default async function SuppressionsPage() {
  const me = await requireStaff();
  const list = items<Suppression>((await serverGetOr404<unknown>("/api/v1/admin/communications/suppressions", "/admin/communications/suppressions")).data);
  const manage = can(me.permissions, "communications.suppression.manage");
  return (
    <AccountPage title="Suppressions" intro="Nothing is sent to a suppressed destination, whatever the preferences and consent say. Suppressions are lifted, never deleted.">
      <CommsBanner />
      <Section id="list" title={`Active suppressions (${list.length})`}>
        {list.length === 0 ? <p className="text-ink-700">No active suppression.</p> : (
          <TableShell caption="Suppressions" head={["Since", "Channel", "Reason", "Source", "Account", "Action"]}>
            {list.map((x) => (
              <tr key={x.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3"><When at={x.created_at} /></td>
                <td className="p-3">{humanise(x.channel)}</td>
                <td className="p-3">{humanise(x.reason)}</td>
                <td className="p-3">{humanise(x.source)}</td>
                <td className="p-3"><ShortId value={x.user_id} /></td>
                <td className="p-3">{manage ? <LiftSuppression id={x.id} /> : "—"}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="add" title="Suppress a destination">
        {manage ? <SuppressForm /> : <NoPermission action="Suppressing a destination" permission="communications.suppression.manage" />}
      </Section>
    </AccountPage>
  );
}
