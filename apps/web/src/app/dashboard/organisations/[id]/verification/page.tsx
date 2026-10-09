import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { OrganisationVerification } from "@/components/verification/organisations";
import { requireUser, serverGet, serverGetOr404, serverGetOptional } from "@/lib/auth/session";
import type { KybStatus, KycStatus, MyOrganisation } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Organisation verification", robots: { index: false, follow: false, nocache: true } };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Members only: the API answers 404 (ORGANISATION_NOT_FOUND) to anyone else, and so does this page. ORG_ADMIN
 * manages the verification; ORG_MEMBER sees its status read-only (the API sends person/document details to
 * administrators only).
 */
export default async function OrganisationVerificationPage({ params }: PageProps<"/dashboard/organisations/[id]/verification">) {
  const { id } = await params;
  const path = `/dashboard/organisations/${encodeURIComponent(id)}/verification`;
  await requireUser(path);
  if (!UUID.test(id)) notFound();
  const org = (await serverGetOr404<MyOrganisation>(`/api/v1/organisations/${encodeURIComponent(id)}`, path)).data;
  const [kyb, kyc] = await Promise.all([
    serverGet<KybStatus>(`/api/v1/organisations/${encodeURIComponent(id)}/kyb`, path),
    serverGetOptional<KycStatus | null>("/api/v1/kyc/status", path, null),
  ]);
  const representativeVerified = kyc?.level === "IDENTITY_VERIFIED" || kyc?.level === "PAYOUT_VERIFIED";
  return (
    <AccountPage title={`Verify ${org.display_name}`} intro="Organisation verification (KYB): registration details, the people behind the organisation, and documents.">
      <OrganisationVerification organisation={org} initial={kyb} representativeVerified={representativeVerified} />
    </AccountPage>
  );
}
