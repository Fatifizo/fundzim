import type { Metadata } from "next";

import { ModuleOperations } from "@/components/admin/operations/module-operations";

export const metadata: Metadata = { title: "Ledger operations", robots: { index: false, follow: false, nocache: true } };

/** Ledger operations (`ledger.view`), read-only: integrity, reconciliation and pending maker-checker adjustments. */
export default function LedgerOperationsPage() {
  return (
    <ModuleOperations
      path="/admin/operations/ledger"
      title="Ledger operations"
      intro="TEST ledger. Integrity checks, open reconciliation items and adjustments waiting for a checker."
      readOnlyNotice="Read-only. The operations portal never writes ledger records; corrections are maker-checker adjustments in the ledger module."
      modules={["ledger"]}
      links={[
        { href: "/admin/finance/ledger", label: "Ledger overview", permission: "ledger.view" },
        { href: "/admin/finance/reconciliation", label: "Ledger reconciliation", permission: "ledger.view" },
        { href: "/admin/finance/exceptions", label: "Ledger exceptions", permission: "ledger.view" },
        { href: "/admin/finance/trial-balance", label: "Trial balance", permission: "ledger.view" },
      ]}
    />
  );
}
