import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { CreatePolicyForm } from "@/components/admin/finance/fee-forms";
import { FEES_BASE, FeeBanner, PoliciesTable } from "@/components/admin/finance/fees";
import { Section } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readFeePage, readPolicy } from "@/lib/fees/normalise";

export const metadata: Metadata = { title: "Fee policies", robots: { index: false, follow: false, nocache: true } };

/** Fee policies (`fee.view`; creating needs `fee.config.request` and step-up). */
export default async function FeePoliciesPage() {
  await requireStaff();
  const page = readFeePage((await serverGetOr404<unknown>("/api/v1/admin/fees/policies", `${FEES_BASE}/policies`)).data, readPolicy);
  return (
    <AccountPage title="Fee policies" intro="A policy is a fixed scope. Its versions carry the terms and pass maker-checker approval before they apply.">
      <FeeBanner />
      <PoliciesTable items={page.items} caption="Fee policies" />
      <Section id="create" title="Create a fee policy" description="Creating a policy charges nothing: a version must be drafted, approved by a different person and activated.">
        <CreatePolicyForm />
      </Section>
    </AccountPage>
  );
}
