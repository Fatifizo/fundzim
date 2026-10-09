import type { Metadata } from "next";
import Link from "next/link";

import { inputClasses } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { ButtonLink, buttonClasses } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Container } from "@/components/ui/container";
import { EmptyState } from "@/components/ui/empty-state";
import { PublicImage } from "@/components/campaigns/public-image";
import { formatGoal } from "@/lib/campaigns/money";
import { first, isCode, isCursor, publicMediaUrl } from "@/lib/campaigns/paths";
import { fetchCategories, fetchPublicList } from "@/lib/campaigns/public";

export const metadata: Metadata = {
  title: "Campaigns",
  description: "Browse published FundZim campaigns. Donations are not yet available.",
};

const PAGE_SIZE = 12;

/** Public listing (GET /public/campaigns): published, paused and completed campaigns that are listed PUBLIC. */
export default async function CampaignsListPage({ searchParams }: PageProps<"/campaigns">) {
  const p = await searchParams;
  const q = (first(p.q) ?? "").trim().slice(0, 100);
  const categoryRaw = first(p.category);
  const category = isCode(categoryRaw) ? categoryRaw : "";
  const sort = first(p.sort) === "created" ? "created" : "published";
  const cursorRaw = first(p.cursor);
  const cursor = isCursor(cursorRaw) ? cursorRaw : "";
  const query = new URLSearchParams();
  if (q) query.set("q", q);
  if (category) query.set("category", category);
  query.set("sort", sort);
  query.set("limit", String(PAGE_SIZE));
  if (cursor) query.set("cursor", cursor);
  const [{ items, meta }, cats] = await Promise.all([fetchPublicList(query.toString()), fetchCategories()]);
  const next = meta.next_cursor && isCursor(meta.next_cursor) ? meta.next_cursor : null;
  const nextQuery = new URLSearchParams(query);
  nextQuery.delete("limit");
  if (next) nextQuery.set("cursor", next);

  return (
    <Container className="py-10 sm:py-14">
      <div className="space-y-6">
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">Campaigns</h1>
          <p className="mt-2 text-lg text-ink-600">Campaigns that FundZim reviewers approved and their organisers published. Donations are not yet available.</p>
        </div>
        <form method="get" action="/campaigns" role="search" aria-label="Search campaigns" className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-4 sm:items-end">
          <div className="space-y-1 sm:col-span-2">
            <label htmlFor="q" className="block font-semibold text-ink-900">Search</label>
            <input id="q" name="q" type="search" defaultValue={q} maxLength={100} className={inputClasses} />
          </div>
          <div className="space-y-1">
            <label htmlFor="category" className="block font-semibold text-ink-900">Category</label>
            <select id="category" name="category" defaultValue={category} className={inputClasses}>
              <option value="">All categories</option>
              {cats.map((c) => (
                <option key={c.code} value={c.code}>{c.name}</option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label htmlFor="sort" className="block font-semibold text-ink-900">Sort by</label>
            <select id="sort" name="sort" defaultValue={sort} className={inputClasses}>
              <option value="published">Recently published</option>
              <option value="created">Recently created</option>
            </select>
          </div>
          <div className="sm:col-span-4">
            <button type="submit" className={buttonClasses("primary")}>Search</button>
          </div>
        </form>
        {items.length === 0 ? (
          <EmptyState title="No campaigns found" description={q || category ? "Try a different search or category." : "There are no published campaigns yet."} />
        ) : (
          <ul className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3" aria-label="Campaigns">
            {items.map((c) => {
              const cover = c.cover ? publicMediaUrl(c.slug, c.cover.id) : null;
              return (
                <Card as="li" key={c.slug} className="flex flex-col gap-3 p-0">
                  {cover ? (
                    <PublicImage src={cover} alt="" width={640} height={360} className="aspect-video w-full rounded-t-card bg-surface-muted object-cover" />
                  ) : (
                    <div aria-hidden="true" className="aspect-video w-full rounded-t-card bg-[linear-gradient(135deg,var(--color-brand-100),var(--color-gold-200))]" />
                  )}
                  <div className="flex flex-1 flex-col gap-2 px-5 pb-5">
                    <p className="flex flex-wrap gap-2">
                      <Badge tone="neutral">{c.category.name || c.category.code}</Badge>
                      {c.status === "PAUSED" ? <Badge tone="gold">Paused</Badge> : null}
                      {c.status === "COMPLETED" ? <Badge tone="neutral">Completed</Badge> : null}
                    </p>
                    <h2 className="font-display text-xl font-semibold break-words text-ink-900">
                      <Link href={`/campaigns/${c.slug}`} className="underline-offset-4 hover:underline">
                        {c.title}
                      </Link>
                    </h2>
                    <p className="break-words text-ink-700">{c.summary}</p>
                    <p className="mt-auto text-sm font-semibold text-ink-700">Goal: {formatGoal(c.goal).text}</p>
                  </div>
                </Card>
              );
            })}
          </ul>
        )}
        {next ? (
          <ButtonLink href={`/campaigns?${nextQuery.toString()}`} variant="outline">
            Next page
          </ButtonLink>
        ) : null}
      </div>
    </Container>
  );
}
