import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { inputClasses } from "@/components/forms/text-field";
import { OpsBanner, Section, SourcesNotice } from "@/components/admin/operations/ops";
import { buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { list, obj, sources } from "@/lib/operations/normalise";
import type { SearchHit } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Search", robots: { index: false, follow: false, nocache: true } };

/**
 * Global search (`ops.dashboard.view`): exact ids and record numbers across the modules the caller may see. No
 * search by email, phone, name or document. The search is audited by kind and result count, never its text.
 */
export default async function SearchPage({ searchParams }: PageProps<"/admin/search">) {
  await requireStaff();
  const p = await searchParams;
  const q = typeof p.q === "string" ? p.q.trim().slice(0, 100) : "";
  const valid = [...q].length >= 3;
  const data = valid ? (await serverGetOr404<unknown>(`/api/v1/admin/operations/search?q=${encodeURIComponent(q)}`, "/admin/search")).data : null;
  const hits = list<SearchHit>(data, "hits");
  const notice = obj(data).notice;
  return (
    <AccountPage title="Search" intro="Find a record by its id, case number or reference. Results only include modules your roles can open.">
      <OpsBanner />
      <form role="search" method="get" action="/admin/search" className="flex flex-col gap-3 rounded-card border border-line bg-surface p-4 sm:flex-row sm:items-end">
        <div className="flex-1 space-y-1">
          <label htmlFor="search-q" className="block font-semibold text-ink-900">
            Id, number or reference
          </label>
          <input id="search-q" name="q" type="search" defaultValue={q} minLength={3} maxLength={100} className={inputClasses} />
        </div>
        <button type="submit" className={buttonClasses("primary")}>
          Search
        </button>
      </form>
      {q && !valid ? <p role="alert" className="font-semibold text-danger-700">Enter at least 3 characters.</p> : null}
      {data ? (
        <>
          <SourcesNotice sources={sources(data)} />
          <Section id="results" title={`Results (${hits.length})`} description={typeof notice === "string" ? notice : undefined}>
            {hits.length === 0 ? (
              <p className="text-ink-700">Nothing found that your roles can open.</p>
            ) : (
              <ul className="space-y-2">
                {hits.map((h) => (
                  <li key={`${h.kind}-${h.id}`} className="rounded-card border border-line bg-surface p-4">
                    <Link href={h.href} className="font-semibold underline underline-offset-4">
                      {h.label}
                    </Link>
                    <span className="ml-2 text-sm text-ink-600">
                      {humanise(h.kind)}
                      {h.status ? ` · ${humanise(h.status)}` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Section>
        </>
      ) : null}
    </AccountPage>
  );
}
