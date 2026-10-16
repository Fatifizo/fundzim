import type { Metadata } from "next";

import { ModuleOperations } from "@/components/admin/operations/module-operations";

export const metadata: Metadata = { title: "Verification operations", robots: { index: false, follow: false, nocache: true } };

/**
 * Verification operations (`kyc.case.review`): KYC, KYB, beneficiary and compliance queues by status and age. No
 * identity document, number or image is shown here; case detail stays in the verification module (audited access).
 */
export default function VerificationOperationsPage() {
  return (
    <ModuleOperations
      path="/admin/operations/verification"
      title="Verification operations"
      intro="Queues and second approvals for identity, organisation and beneficiary verification, and compliance cases. Identity documents are never shown here."
      modules={["kyc", "beneficiaries", "compliance"]}
      links={[
        { href: "/admin/verification", label: "Verification queue", permission: "kyc.case.review" },
        { href: "/admin/verification/beneficiaries", label: "Beneficiaries", permission: "kyc.case.review" },
        { href: "/admin/compliance/cases", label: "Compliance cases", permission: "compliance.case.view" },
      ]}
    />
  );
}
