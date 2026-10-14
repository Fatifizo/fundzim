import { notFound, redirect } from "next/navigation";

import { withdrawalHref } from "@/components/admin/finance/payouts";
import { requireStaff } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";

/** A payout is its withdrawal request: one detail page. */
export default async function PayoutPage({ params }: PageProps<"/admin/finance/payouts/[id]">) {
  await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  redirect(withdrawalHref(id));
}
