import type { Metadata } from "next";

import { ModuleOperations } from "@/components/admin/operations/module-operations";

export const metadata: Metadata = { title: "Payout operations", robots: { index: false, follow: false, nocache: true } };

/** Payout and fee operations (`payout.view`): TEST withdrawals, approvals, reservations, exceptions and fee reconciliation. */
export default function PayoutOperationsPage() {
  return (
    <ModuleOperations
      path="/admin/operations/payouts"
      title="Payout operations"
      intro="TEST payouts through the simulator only; no real payout is executed. An unknown payout keeps its reservation until its outcome is known. Fee figures are TEST fees."
      modules={["payouts", "fees"]}
      links={[
        { href: "/admin/finance/withdrawals", label: "Withdrawal requests", permission: "payout.view" },
        { href: "/admin/finance/payouts/approvals", label: "Payout approvals", permission: "payout.view" },
        { href: "/admin/finance/payouts/exceptions", label: "Payout exceptions", permission: "payout.view" },
        { href: "/admin/finance/payouts/reservations", label: "Reservations", permission: "payout.view" },
        { href: "/admin/finance/fees/reconciliation", label: "Fee reconciliation", permission: "fee.view" },
      ]}
    />
  );
}
