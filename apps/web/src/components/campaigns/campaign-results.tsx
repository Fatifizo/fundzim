import { TrackEvent } from "@/components/analytics/track-event";
import { CampaignGrid } from "@/components/campaigns/campaign-card";
import { ButtonLink } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { resultCountText, type ListQuery } from "@/lib/campaigns/list-query";
import { isCursor } from "@/lib/campaigns/paths";
import { fetchPublicList, type PublicListResult } from "@/lib/campaigns/public";

export const PAGE_SIZE = 12;


export function apiQuery(query: ListQuery): URLSearchParams {
  const params = new URLSearchParams();
  if (query.q) params.set("q", query.q);
  if (query.category) params.set("category", query.category);
  params.set("sort", query.sort);
  params.set("limit", String(PAGE_SIZE));
  if (query.cursor) params.set("cursor", query.cursor);
  return params;
}

/** Page URL for this listing (the category page keeps its own path and omits the category parameter). */
export function pageHref(basePath: string, query: ListQuery, cursor: string | null, includeCategory: boolean): string {
  const params = new URLSearchParams();
  if (query.q) params.set("q", query.q);
  if (includeCategory && query.category) params.set("category", query.category);
  if (query.sort !== "published") params.set("sort", query.sort);
  if (cursor) params.set("cursor", cursor);
  return params.size ? `${basePath}?${params.toString()}` : basePath;
}

/**
 * One page of public campaigns (server-rendered, `cache: 'no-store'`), with the exact (or capped) result
 * count, keyset pagination ("Next page" follows the API cursor; "Back to the first page" restarts) and
 * empty/error states. Search analytics records only that a search happened, never the query text.
 */
export async function CampaignResults({ query, basePath, includeCategory, trackSearch }: { query: ListQuery; basePath: string; includeCategory: boolean; trackSearch: boolean }) {
  let result: PublicListResult;
  try {
    result = await fetchPublicList(apiQuery(query));
  } catch {
    return (
      <ErrorState
        title="Campaigns could not be loaded"
        description="Something went wrong while loading campaigns. Please try again in a moment."
        action={
          <ButtonLink href={pageHref(basePath, query, query.cursor || null, includeCategory)} variant="outline">
            Try again
          </ButtonLink>
        }
      />
    );
  }
  const next = result.nextCursor && isCursor(result.nextCursor) ? result.nextCursor : null;
  const filtered = query.q !== "" || (includeCategory && query.category !== "");
  return (
    <div className="space-y-6">
      {trackSearch && query.q && !query.cursor ? <TrackEvent event={{ type: "CAMPAIGN_SEARCH", ...(query.category ? { category: query.category } : {}) }} /> : null}
      <p role="status" className="font-semibold text-ink-700">
        {result.total ? resultCountText(result.total) : `${result.items.length} campaign${result.items.length === 1 ? "" : "s"} on this page`}
        {query.cursor ? " · later page" : ""}
      </p>
      {result.items.length === 0 ? (
        <EmptyState
          title="No campaigns found"
          description={filtered ? "Try different words, or clear the filters to see every published campaign." : "There are no published campaigns here yet. Every campaign is reviewed before it is published."}
          action={
            filtered ? (
              <ButtonLink href={includeCategory ? "/campaigns" : basePath} variant="outline">
                Clear search
              </ButtonLink>
            ) : undefined
          }
        />
      ) : (
        <CampaignGrid campaigns={result.items} label="Campaigns" headingLevel={2} priorityFirst />
      )}
      {next || query.cursor ? (
        <nav aria-label="Pagination" className="flex flex-wrap gap-3">
          {query.cursor ? (
            <ButtonLink href={pageHref(basePath, query, null, includeCategory)} variant="ghost">
              Back to the first page
            </ButtonLink>
          ) : null}
          {next ? (
            <ButtonLink href={pageHref(basePath, query, next, includeCategory)} variant="outline">
              Next page
            </ButtonLink>
          ) : null}
        </nav>
      ) : null}
    </div>
  );
}
