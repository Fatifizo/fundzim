import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FEES_BASE, FeeBanner, QuotesTable } from "@/components/admin/finance/fees";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readFeePage, readQuote } from "@/lib/fees/normalise";

export const metadata: Metadata = { title: "Fee quotes", robots: { index: false, follow: false, nocache: true } };

/** Fee quotes (`fee.view`). A quote is not a charge and not revenue. */
export default async function FeeQuotesPage() {
  await requireStaff();
  const page = readFeePage((await serverGetOr404<unknown>("/api/v1/admin/fees/quotes?limit=100", `${FEES_BASE}/quotes`)).data, readQuote);
  return (
    <AccountPage title="Fee quotes" intro="Quotes for TEST donations. A quote is not a charge and not revenue.">
      <FeeBanner />
      <QuotesTable items={page.items} caption="Fee quotes" />
    </AccountPage>
  );
}
