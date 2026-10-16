import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, When } from "@/components/admin/payments/common";
import { AlertsList, MetricsGrid, OpsBanner, Section, SourcesNotice } from "@/components/admin/operations/ops";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { can } from "@/lib/operations/navigation";
import { list, metrics, obj, sources } from "@/lib/operations/normalise";
import type { Alert, Tasks } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Operations command center", robots: { index: false, follow: false, nocache: true } };

/**
 * Operations command center (`ops.dashboard.view`): the caller's tasks, alerts and every metric their permissions
 * allow, each read from the owning module's own records and labelled with its source and nature. A module that cannot
 * be read shows as unavailable — never as zero.
 */
export default async function OperationsPage() {
  const me = await requireStaff();
  const data = (await serverGetOr404<unknown>("/api/v1/admin/operations/overview", "/admin/operations")).data;
  const o = obj(data);
  const t = obj(o.tasks) as Partial<Tasks>;
  const n = (v: number | undefined) => String(v ?? 0);
  const alerts = list<Alert>(data, "alerts");
  return (
    <AccountPage title="Operations command center" intro="What needs attention across FundZim, from each module's own records.">
      <OpsBanner notice={typeof o.notice === "string" ? o.notice : undefined} />
      <SourcesNotice sources={sources(data)} />
      <Section id="my-tasks" title="My tasks">
        <Facts
          items={[
            ["Approvals I can decide", <Link key="a" href="/admin/approvals?decidable=true" className="underline underline-offset-4">{n(t.approvals_to_decide)}</Link>],
            ["Support cases assigned to me", <Link key="s" href="/admin/support?assigned_to=me" className="underline underline-offset-4">{n(t.support_cases_assigned)}</Link>],
            ["My support cases past their internal target", n(t.support_cases_overdue)],
            ["Exceptions assigned to me", <Link key="e" href="/admin/operations/exceptions?assigned_to=me" className="underline underline-offset-4">{n(t.exceptions_assigned)}</Link>],
            ["Incidents I own", n(t.incidents_owned)],
            ["Access review items waiting for me", n(t.access_review_items_pending)],
            ["Open incidents", n(t.open_incidents)],
            ["Generated", <When key="g" at={typeof o.generated_at === "string" ? o.generated_at : null} />],
          ]}
        />
      </Section>
      <Section id="alerts" title="Alerts" description="Raised from recorded evidence (workers, outbox, ledger integrity, risk, providers). An alert never changes data.">
        <AlertsList alerts={alerts} />
        {can(me.permissions, "ops.incident.manage") && alerts.some((a) => !a.incident_id) ? (
          <p>
            <Link href="/admin/operations/health#alerts" className="underline underline-offset-4">
              Declare an incident from an alert
            </Link>
          </p>
        ) : null}
      </Section>
      <Section id="metrics" title="Metrics" description="Counts of records, not money. TEST and simulated figures are labelled and never business performance.">
        <MetricsGrid metrics={metrics(data)} />
      </Section>
    </AccountPage>
  );
}
