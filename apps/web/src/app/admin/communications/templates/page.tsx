import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { Code, FilterForm, TableShell, toOptions } from "@/components/admin/payments/common";
import { CommsBanner, templateHref } from "@/components/admin/communications/comms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { items } from "@/lib/operations/normalise";
import { CATEGORIES, type Template } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Notification templates", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/communications/templates";

/** Notification templates (`communications.view`). Versions are maker-checker; English is the baseline (no translation exists). */
export default async function TemplatesPage({ searchParams }: PageProps<"/admin/communications/templates">) {
  await requireStaff();
  const p = await searchParams;
  const category = pick(p.category, CATEGORIES);
  const list = items<Template>((await serverGetOr404<unknown>(`/api/v1/admin/communications/templates${buildQuery({ category })}`, BASE)).data);
  return (
    <AccountPage title="Notification templates" intro="Each notification is rendered from the template version that was active when it was created.">
      <CommsBanner />
      <FilterForm action={BASE} label="Filter templates" fields={[{ name: "category", label: "Category", value: category, options: toOptions(CATEGORIES) }]} />
      <TableShell caption="Templates" head={["Template", "Category", "Active version", "Pending", "Origin"]}>
        {list.map((t) => (
          <tr key={t.id} className="border-b border-line align-top last:border-b-0">
            <td className="p-3"><Link href={templateHref(t.id)} className="underline underline-offset-4"><Code>{t.key}</Code></Link></td>
            <td className="p-3">{humanise(t.category)}</td>
            <td className="p-3">{t.active_version ?? "None"}</td>
            <td className="p-3">{t.pending_status ? humanise(t.pending_status) : "—"}</td>
            <td className="p-3">{humanise(t.origin)}</td>
          </tr>
        ))}
      </TableShell>
    </AccountPage>
  );
}
