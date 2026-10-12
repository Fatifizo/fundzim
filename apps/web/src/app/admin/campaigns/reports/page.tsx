import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { ReportQueue } from "@/components/admin/report-queue";
import { Alert } from "@/components/ui/alert";
import { ButtonLink } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor, readStaffReports } from "@/lib/campaigns/normalise";
import { first, isCursor } from "@/lib/campaigns/paths";
import { REPORT_STATUSES, type ReportStatus } from "@/lib/campaigns/types";

export const metadata: Metadata = { title: "Campaign reports", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/campaigns/reports";
const LABELS: Record<ReportStatus, string> = { OPEN: "Open", ESCALATED: "Escalated", DISMISSED: "Dismissed" };

/** Reports from the public (`campaign.review`; 404 for anyone else). */
export default async function CampaignReportsPage({ searchParams }: PageProps<"/admin/campaigns/reports">) {
  await requireStaff();
  const p = await searchParams;
  const statusRaw = first(p.status);
  const status: ReportStatus = (REPORT_STATUSES as readonly string[]).includes(statusRaw ?? "") ? (statusRaw as ReportStatus) : "OPEN";
  const cursorRaw = first(p.cursor);
  const params = new URLSearchParams({ status });
  if (isCursor(cursorRaw)) params.set("cursor", cursorRaw);
  const result = await serverGetOr404<unknown>(`/api/v1/admin/campaigns/reports?${params.toString()}`, BASE);
  const items = readStaffReports(result.data);
  const nextRaw = readNextCursor(result.data, result.meta);
  const next = nextRaw && isCursor(nextRaw) ? nextRaw : null;
  return (
    <AccountPage title="Campaign reports" intro="Reports sent by visitors from campaign pages, oldest first.">
      <Alert tone="info" title="A report never suspends a campaign by itself">
        Dismissing closes the report. Escalating opens a compliance case. Neither changes the campaign: suspending it is a separate review
        decision on the campaign&apos;s review page.
      </Alert>
      <nav aria-label="Report status" className="flex flex-wrap gap-2">
        {REPORT_STATUSES.map((s) => (
          <Link
            key={s}
            href={`${BASE}?status=${s}`}
            aria-current={s === status ? "page" : undefined}
            className="inline-flex min-h-11 items-center rounded-full border border-line px-4 font-medium text-ink-700 hover:bg-brand-50 aria-[current=page]:border-brand-700 aria-[current=page]:bg-brand-50 aria-[current=page]:text-brand-800"
          >
            {LABELS[s]}
          </Link>
        ))}
      </nav>
      <ReportQueue items={items} />
      {next ? (
        <ButtonLink href={`${BASE}?status=${status}&cursor=${encodeURIComponent(next)}`} variant="outline">
          Next page
        </ButtonLink>
      ) : null}
    </AccountPage>
  );
}
