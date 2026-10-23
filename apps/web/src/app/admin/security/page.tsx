import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, FilterForm, When } from "@/components/admin/payments/common";
import { OpsBanner, Section } from "@/components/admin/operations/ops";
import { AlertsTable } from "@/components/admin/operations/security";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { items, obj } from "@/lib/operations/normalise";
import { ALERT_SEVERITIES, ALERT_STATUSES, type BackupEvidence, type SecurityAlert } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Security alerts", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/security";

/** Stage 18 security alerts and backup evidence (`security.alert.view`). */
export default async function SecurityAlertsPage({ searchParams }: PageProps<"/admin/security">) {
  await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, ALERT_STATUSES);
  const minSeverity = pick(p.min_severity, ALERT_SEVERITIES);
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/security/alerts${buildQuery({ status, min_severity: minSeverity })}`, BASE)).data;
  const list = items<SecurityAlert>(data);
  const backups = obj((await serverGetOr404<unknown>("/api/v1/admin/security/backups", BASE)).data) as unknown as BackupEvidence;
  return (
    <AccountPage
      title="Security alerts"
      intro="Raised every minute by fixed rules over each module's own records. Thresholds are engineering defaults, not service levels. Alerts stay internal: no notification leaves FundZim."
    >
      <OpsBanner />
      <FilterForm
        action={BASE}
        label="Filter alerts"
        fields={[
          { name: "status", label: "Status", value: status, options: ALERT_STATUSES.map((s) => ({ value: s, label: s === "LIVE" ? "Open or acknowledged" : humanise(s) })) },
          { name: "min_severity", label: "At least", value: minSeverity, options: ALERT_SEVERITIES.map((s) => ({ value: s, label: humanise(s) })) },
        ]}
      />
      <Section id="alerts" title={`Alerts (${list.length})`}>
        <AlertsTable items={list} caption="Security alerts" />
      </Section>
      <Section id="backups" title="Backup and restore evidence">
        <Facts
          items={[
            ["Last successful backup", backups.last_backup ? <When key="b" at={backups.last_backup} /> : "None recorded"],
            ["Last failed backup", backups.last_failed_backup ? <When key="f" at={backups.last_failed_backup} /> : "None recorded"],
            ["Last verified restore drill", backups.last_verified_restore ? <When key="r" at={backups.last_verified_restore} /> : "None recorded"],
            ["Recorded runs", String(backups.runs ?? 0)],
          ]}
        />
      </Section>
    </AccountPage>
  );
}
