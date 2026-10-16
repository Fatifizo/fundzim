import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { ApprovalsTable, ExceptionsTable, MetricsGrid, OpsBanner, Section, SourcesNotice } from "@/components/admin/operations/ops";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { can } from "@/lib/operations/navigation";
import { items, metrics, sources } from "@/lib/operations/normalise";
import type { Approval, ExceptionItem } from "@/lib/operations/types";

export interface ModuleOperationsProps {
  path: string;
  title: string;
  intro: string;
  /** The modules whose snapshots this page shows (internal/app NewOperations source names). */
  modules: readonly string[];
  /** The owning module's own pages, where every decision and correction is made. */
  links: ReadonlyArray<{ href: string; label: string; permission: string }>;
  readOnlyNotice?: string;
}

/**
 * A module's operations view (Stage 14 §24–37): its metrics, pending approvals and open exceptions from the module's
 * own snapshot, with links to the module's pages. Nothing is decided or corrected here; the module owns its records.
 */
export async function ModuleOperations({ path, title, intro, modules, links, readOnlyNotice }: ModuleOperationsProps) {
  const me = await requireStaff();
  const [overview, approvals, exceptions] = await Promise.all([
    serverGetOr404<unknown>("/api/v1/admin/operations/overview", path).then((r) => r.data),
    serverGetOptional<unknown>("/api/v1/admin/approvals", path, null),
    serverGetOptional<unknown>("/api/v1/admin/operations/exceptions", path, null),
  ]);
  const mine = (m: string) => modules.includes(m);
  const ms = metrics(overview).filter((m) => mine(m.module));
  const src = sources(overview).filter((s) => mine(s.module));
  const ap = items<Approval>(approvals).filter((a) => mine(a.source_module));
  const ex = items<ExceptionItem>(exceptions).filter((e) => mine(e.source_module));
  const visibleLinks = links.filter((l) => can(me.permissions, l.permission));
  return (
    <AccountPage title={title} intro={intro}>
      <OpsBanner />
      {readOnlyNotice ? <p className="rounded-card border border-line bg-surface-muted p-4 text-ink-700">{readOnlyNotice}</p> : null}
      <SourcesNotice sources={src} />
      {approvals === null || exceptions === null ? (
        <p role="status" className="rounded-card border border-line bg-surface p-4 text-ink-700">
          Some lists could not be loaded. Reload the page to try again.
        </p>
      ) : null}
      {visibleLinks.length > 0 ? (
        <Section id="pages" title="Work in the module">
          <ul className="flex flex-wrap gap-3">
            {visibleLinks.map((l) => (
              <li key={l.href}>
                <Link href={l.href} className="inline-flex min-h-11 items-center rounded-full border border-line px-4 font-medium underline-offset-4 hover:underline">
                  {l.label}
                </Link>
              </li>
            ))}
          </ul>
        </Section>
      ) : null}
      <Section id="metrics" title="Metrics">
        <MetricsGrid metrics={ms} />
      </Section>
      <Section id="approvals" title="Waiting for a decision" description="Decisions are made in the owning module, which re-checks permissions, state, step-up and separation of duties.">
        <ApprovalsTable items={ap} caption={`${title}: pending approvals`} />
      </Section>
      <Section id="exceptions" title="Open exceptions">
        <ExceptionsTable items={ex} caption={`${title}: open exceptions`} canWork={can(me.permissions, "ops.exception.work")} />
      </Section>
    </AccountPage>
  );
}
