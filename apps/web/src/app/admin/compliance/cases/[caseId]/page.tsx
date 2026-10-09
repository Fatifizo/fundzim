import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { ComplianceCaseView } from "@/components/admin/compliance-case";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { normaliseComplianceCase } from "@/lib/verification/normalise";

export const metadata: Metadata = { title: "Compliance case", robots: { index: false, follow: false, nocache: true } };

const ID = /^[A-Za-z0-9-]{1,64}$/;

export default async function ComplianceCasePage({ params }: PageProps<"/admin/compliance/cases/[caseId]">) {
  const me = await requireStaff();
  const { caseId } = await params;
  if (!ID.test(caseId)) notFound();
  const path = `/admin/compliance/cases/${encodeURIComponent(caseId)}`;
  const raw = (await serverGetOr404<unknown>(`/api/v1/admin/compliance/cases/${encodeURIComponent(caseId)}`, path)).data;
  const complianceCase = normaliseComplianceCase(raw, me.id);
  return (
    <AccountPage title={`Compliance case ${complianceCase.reference ?? complianceCase.id.slice(0, 8)}`} intro={<Link href="/admin/compliance/cases" className="text-base font-semibold text-brand-700 underline">Back to compliance cases</Link>}>
      <ComplianceCaseView complianceCase={complianceCase} meId={me.id} />
    </AccountPage>
  );
}
