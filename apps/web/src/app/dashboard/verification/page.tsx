import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { CaseStatusBadge, DefinitionList, ProgressList } from "@/components/verification/status";
import { Badge } from "@/components/ui/badge";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { requireUser, serverGet } from "@/lib/auth/session";
import { kycNextStep, kycProgress, levelLabel, profileStatusLabel } from "@/lib/verification/labels";
import type { KycStatus } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Verification", robots: { index: false, follow: false, nocache: true } };

const GATES: Array<{ key: keyof KycStatus["gates"]; label: string; note?: string }> = [
  { key: "create_draft", label: "Create campaign drafts" },
  { key: "submit_campaign", label: "Submit a campaign for review" },
  { key: "withdraw", label: "Request payouts", note: "Payouts are not available yet in any case." },
];

export default async function VerificationOverviewPage() {
  await requireUser("/dashboard/verification");
  const status = await serverGet<KycStatus>("/api/v1/kyc/status", "/dashboard/verification");
  const next = kycNextStep(status.case, status.level);
  const steps = kycProgress(status.case);
  return (
    <AccountPage title="Verification" intro="Confirm who you are so you can raise funds on FundZim.">
      <Card as="section" aria-labelledby="level-title">
        <h2 id="level-title" className="font-display text-xl font-semibold text-ink-900">
          Your verification
        </h2>
        <div className="mt-4">
          <DefinitionList
            items={[
              { term: "Verification level", value: levelLabel(status.level) },
              { term: "Account status", value: profileStatusLabel(status.status) },
              { term: "Current verification", value: <CaseStatusBadge status={status.case?.status} /> },
            ]}
          />
        </div>
      </Card>

      <Card as="section" aria-labelledby="progress-title">
        <h2 id="progress-title" className="font-display text-xl font-semibold text-ink-900">
          Progress
        </h2>
        <div className="mt-4">
          <ProgressList steps={steps} label="Identity verification progress" />
        </div>
      </Card>

      <Card as="section" aria-labelledby="next-title">
        <h2 id="next-title" className="font-display text-xl font-semibold text-ink-900">
          What to do next: {next.title}
        </h2>
        {next.description ? <p className="mt-2 text-ink-700">{next.description}</p> : null}
        {next.href && next.action ? (
          <ButtonLink href={next.href} className="mt-4">
            {next.action}
          </ButtonLink>
        ) : null}
      </Card>

      <Card as="section" aria-labelledby="gates-title">
        <h2 id="gates-title" className="font-display text-xl font-semibold text-ink-900">
          What you can do now
        </h2>
        <ul className="mt-3 space-y-2">
          {GATES.map((gate) => (
            <li key={gate.key} className="flex flex-wrap items-center gap-2 text-ink-700">
              {status.gates?.[gate.key] ? <Badge tone="brand">Allowed</Badge> : <Badge tone="neutral">Not yet</Badge>}
              <span>{gate.label}</span>
              {gate.note ? <span className="text-sm text-ink-600">({gate.note})</span> : null}
            </li>
          ))}
        </ul>
      </Card>

      <Card as="section" aria-labelledby="more-title">
        <h2 id="more-title" className="font-display text-xl font-semibold text-ink-900">
          Other verifications
        </h2>
        <ul className="mt-3 list-disc space-y-2 pl-6 text-ink-700">
          <li>
            <Link href="/dashboard/verification/beneficiaries" className="font-semibold text-brand-700 underline">
              Beneficiaries
            </Link>{" "}
            — the people or organisations you raise funds for.
          </li>
          <li>
            <Link href="/dashboard/verification/payout-destinations" className="font-semibold text-brand-700 underline">
              Payout accounts
            </Link>{" "}
            — where funds would be sent. Payouts are not available yet.
          </li>
          <li>
            <Link href="/dashboard/organisations" className="font-semibold text-brand-700 underline">
              Organisations
            </Link>{" "}
            — verify an organisation you manage.
          </li>
        </ul>
      </Card>
    </AccountPage>
  );
}
