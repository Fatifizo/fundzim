import type { Metadata } from "next";

import { ModuleOperations } from "@/components/admin/operations/module-operations";

export const metadata: Metadata = { title: "Payment operations", robots: { index: false, follow: false, nocache: true } };

/** Payment and provider operations (`payment.view`): TEST payments, unknown outcomes, webhooks and derived provider health. */
export default function PaymentOperationsPage() {
  return (
    <ModuleOperations
      path="/admin/operations/payments"
      title="Payment operations"
      intro="TEST payments only. Unknown outcomes stay unknown until a verified webhook, status query or reconciliation resolves them; provider health is derived from recorded observations."
      modules={["payments", "providers"]}
      links={[
        { href: "/admin/payments/unknown", label: "Unknown payments", permission: "payment.view" },
        { href: "/admin/payments/webhooks", label: "Payment webhooks", permission: "payment.view" },
        { href: "/admin/payments/reconciliation", label: "Payment reconciliation", permission: "payment.view" },
        { href: "/admin/payments/refunds", label: "Refund requests", permission: "payment.view" },
        { href: "/admin/payments/health", label: "Provider health", permission: "payment.view" },
      ]}
    />
  );
}
