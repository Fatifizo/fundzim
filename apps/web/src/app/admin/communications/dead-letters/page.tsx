import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { Code, FilterForm, Section, TableShell, When } from "@/components/admin/payments/common";
import { NoPermission } from "@/components/admin/operations/ops";
import { CommsBanner, deliveryHref } from "@/components/admin/communications/comms";
import { DeadLetterActions } from "@/components/admin/communications/comms-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items } from "@/lib/operations/normalise";
import type { DeadLetter } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Dead letters", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/communications/dead-letters";
const STATUSES = ["OPEN", "REPLAYED", "DISCARDED", "ALL"] as const;

/** Dead-lettered deliveries (`communications.view`; replay and discard need `communications.replay` and step-up). */
export default async function DeadLettersPage({ searchParams }: PageProps<"/admin/communications/dead-letters">) {
  const me = await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, STATUSES) ?? "ALL"; // all by default: a replayed row stays visible with its confirmation
  const list = items<DeadLetter>((await serverGetOr404<unknown>(`/api/v1/admin/communications/dead-letters${buildQuery({ status })}`, BASE)).data);
  const replay = can(me.permissions, "communications.replay");
  return (
    <AccountPage title="Dead letters" intro="Deliveries that ran out of retries, failed permanently or whose outcome stayed unknown. A replay checks everything again before sending.">
      <CommsBanner />
      <FilterForm action={BASE} label="Filter dead letters" fields={[{ name: "status", label: "Review", value: status, options: STATUSES.map((s) => ({ value: s, label: humanise(s) })) }]} />
      <Section id="list" title={`Dead letters (${list.length})`}>
        {list.length === 0 ? <p className="text-ink-700">No dead letters.</p> : (
          <TableShell caption="Dead letters" head={["Since", "Category", "Template", "Channel", "Attempts", "Review", "Action"]} minWidthClass="min-w-[60rem]">
            {list.map((x) => (
              <tr key={x.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3"><Link href={deliveryHref(x.delivery_id)} className="underline underline-offset-4"><When at={x.created_at} /></Link></td>
                <td className="p-3">{humanise(x.failure_category)}{x.last_error ? <div className="text-sm">{x.last_error}</div> : null}</td>
                <td className="p-3"><Code>{x.delivery.template_key}</Code></td>
                <td className="p-3">{humanise(x.delivery.channel)}</td>
                <td className="p-3">{x.attempt_count}</td>
                <td className="p-3">{humanise(x.review_status)}</td>
                <td className="p-3">{replay ? <DeadLetterActions id={x.id} open={x.review_status === "OPEN"} /> : x.review_status === "OPEN" ? <NoPermission action="Replaying" permission="communications.replay" /> : "—"}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
