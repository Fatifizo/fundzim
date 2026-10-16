import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, When } from "@/components/admin/payments/common";
import { HistoryList, OpsBanner, Section, SevBadge, ShortId } from "@/components/admin/operations/ops";
import { TriageActions } from "@/components/admin/operations/ops-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise } from "@/lib/payments/labels";
import { items } from "@/lib/operations/normalise";
import { EXCEPTION_MODULES, type ExceptionItem, type HistoryEntry } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Exception triage", robots: { index: false, follow: false, nocache: true } };

/** One exception's triage (`ops.exception.work`): owner, status, notes and history. */
export default async function ExceptionTriagePage({ params }: PageProps<"/admin/operations/exceptions/[module]/[ref]">) {
  const me = await requireStaff();
  const { module, ref } = await params;
  const id = decodeURIComponent(ref);
  if (!(EXCEPTION_MODULES as readonly string[]).includes(module)) notFound();
  const here = `/admin/operations/exceptions/${encodeURIComponent(module)}/${encodeURIComponent(id)}`;
  const [hist, list] = await Promise.all([
    serverGetOr404<unknown>(`/api/v1/admin/operations/exceptions/${encodeURIComponent(module)}/${encodeURIComponent(id)}/history`, here).then((r) => r.data),
    serverGetOr404<unknown>(`/api/v1/admin/operations/exceptions${buildQuery({ module })}`, here).then((r) => r.data),
  ]);
  const x = items<ExceptionItem>(list).find((e) => e.id === id);
  const history = items<HistoryEntry>(hist);
  if (!x && history.length === 0) notFound();
  return (
    <AccountPage title="Exception triage" intro={x ? x.summary : "This exception is no longer reported by its module."}>
      <OpsBanner />
      {x ? (
        <>
          <Section id="facts" title="Exception">
            <Facts
              items={[
                ["Severity", <SevBadge key="s" severity={x.severity} />],
                ["Module", humanise(x.source_module)],
                ["Category", humanise(x.category)],
                ["Domain status", x.domain_open ? humanise(x.domain_status) : "No longer reported by the module"],
                ["Since", <When key="w" at={x.created_at} />],
                ["Triage", humanise(x.triage_status)],
                ["Assigned to", x.assigned_to ? <ShortId key="a" value={x.assigned_to} /> : "Nobody"],
                ["Open in the module", <Link key="l" href={x.href} className="underline underline-offset-4">{humanise(x.source_module)}</Link>],
              ]}
            />
          </Section>
          <Section id="triage" title="Triage" description="Resolving the triage is refused while the module still reports the exception.">
            <TriageActions module={module} exceptionId={x.id} status={x.triage_status} version={x.triage_version} staffId={me.id} />
          </Section>
        </>
      ) : null}
      <Section id="history" title="History">
        <HistoryList items={history} caption="Triage history" />
      </Section>
    </AccountPage>
  );
}
