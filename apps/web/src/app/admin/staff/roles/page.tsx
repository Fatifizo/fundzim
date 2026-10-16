import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Code } from "@/components/admin/payments/common";
import { OpsBanner, Section } from "@/components/admin/operations/ops";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { items } from "@/lib/operations/normalise";
import type { RoleInventoryItem } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Roles and permissions", robots: { index: false, follow: false, nocache: true } };

/** Role inventory (`staff.view`): each role's permissions and holder count. There is no unrestricted superuser role. */
export default async function RolesPage() {
  await requireStaff();
  const roles = items<RoleInventoryItem>((await serverGetOr404<unknown>("/api/v1/admin/staff/roles", "/admin/staff/roles")).data);
  return (
    <AccountPage title="Roles and permissions" intro="Permissions are granted through roles only. No role includes every permission, and admin roles do not include KYC document access or ledger adjustments.">
      <OpsBanner />
      {roles.map((r) => (
        <Section key={r.code} id={`role-${r.code}`} title={`${r.name} (${r.holders} ${r.holders === 1 ? "holder" : "holders"})`}>
          <ul className="flex flex-wrap gap-2">
            {r.permissions.map((p) => (
              <li key={p}>
                <Code>{p}</Code>
              </li>
            ))}
          </ul>
        </Section>
      ))}
    </AccountPage>
  );
}
