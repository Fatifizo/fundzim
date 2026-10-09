import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { DocumentManager } from "@/components/verification/document-manager";
import { CaseStatusBadge } from "@/components/verification/status";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { requireUser, serverGet } from "@/lib/auth/session";
import { isEditable } from "@/lib/verification/labels";
import { FINAL_CASE_STATUSES, type KycStatus } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Identity documents", robots: { index: false, follow: false, nocache: true } };

export default async function DocumentsPage() {
  await requireUser("/dashboard/verification/documents");
  const status = await serverGet<KycStatus>("/api/v1/kyc/status", "/dashboard/verification/documents");
  const kycCase = status.case && !FINAL_CASE_STATUSES.has(status.case.status) ? status.case : null;
  return (
    <AccountPage title="Identity documents" intro="Upload photos or scans of your ID. Files are checked for security before anyone can open them.">
      {!kycCase ? (
        <EmptyState
          title="Start your verification first"
          description="Documents belong to a verification. Start one, then come back to upload your documents."
          action={<ButtonLink href="/dashboard/verification/identity">Go to your identity</ButtonLink>}
        />
      ) : (
        <Card>
          <p className="mb-4 flex flex-wrap items-center gap-2 text-ink-700">
            <span className="font-semibold">Verification status:</span> <CaseStatusBadge status={kycCase.status} />
          </p>
          {!isEditable(kycCase.status) ? (
            <p className="mb-4 text-ink-700">Documents can&apos;t be added or removed while your verification is being reviewed.</p>
          ) : null}
          <DocumentManager
            subjectType="KYC_CASE"
            subjectId={kycCase.id}
            initialDocuments={kycCase.documents}
            requirements={kycCase.requirements}
            editable={isEditable(kycCase.status)}
            heading="Your documents"
          />
          {isEditable(kycCase.status) ? (
            <ButtonLink href="/dashboard/verification/identity#review" variant="outline" className="mt-6">
              Continue to check and submit
            </ButtonLink>
          ) : null}
        </Card>
      )}
    </AccountPage>
  );
}
