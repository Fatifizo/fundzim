import { CampaignGrid } from "@/components/campaigns/campaign-card";
import { Alert } from "@/components/ui/alert";
import { ButtonLink } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import type { ClickSurface } from "@/lib/analytics/events";
import { resultCountText } from "@/lib/campaigns/list-query";
import { discoveryHref, isFiltered, type DiscoveryQuery } from "@/lib/discovery/query";
import { fetchDiscoveryPage } from "@/lib/discovery/server";
import type { DiscoveryPage } from "@/lib/discovery/types";

export const DISCOVERY_PAGE_SIZE = 12;

/**
 * One page of discovery results (server-rendered, `cache: 'no-store'`): count (exact or "1000+"), a notice when only
 * close matches were found, keyset pagination ("Next page" follows the API cursor; "Back to the first page" restarts),
 * empty and error states. The API re-checks every campaign's eligibility; a page can therefore hold fewer than 12.
 */
export async function DiscoveryResults({
  query,
  basePath,
  fixed = [],
  surface,
  clearHref,
}: {
  query: DiscoveryQuery;
  basePath: string;
  fixed?: ReadonlyArray<keyof DiscoveryQuery>;
  surface: ClickSurface;
  clearHref?: string;
}) {
  let page: DiscoveryPage;
  try {
    page = await fetchDiscoveryPage(query, DISCOVERY_PAGE_SIZE, query.q ? "search" : "campaigns");
  } catch {
    return (
      <ErrorState
        title="Campaigns could not be loaded"
        description="Something went wrong while loading campaigns, or your connection dropped. Please try again in a moment."
        action={
          <ButtonLink href={discoveryHref(basePath, query, {}, fixed)} variant="outline">
            Try again
          </ButtonLink>
        }
      />
    );
  }
  const filtered = isFiltered(query, fixed);
  const next = page.nextCursor;
  return (
    <div className="space-y-6">
      <p role="status" className="font-semibold text-ink-700">
        {page.match === "FUZZY"
          ? `${page.items.length} close match${page.items.length === 1 ? "" : "es"}`
          : page.total
            ? resultCountText(page.total)
            : `${page.items.length} campaign${page.items.length === 1 ? "" : "s"} on this page`}
        {query.cursor ? " · later page" : ""}
      </p>
      {page.match === "FUZZY" && page.items.length > 0 ? (
        <Alert tone="info" title="No exact matches">
          Nothing matched “{query.q}” exactly, so these are campaigns with similar titles. Check the spelling or try other words.
        </Alert>
      ) : null}
      {page.items.length === 0 ? (
        <EmptyState
          title="No campaigns found"
          description={filtered ? "Try different words, or clear the filters to see every published campaign." : "There are no published campaigns here yet. Every campaign is reviewed before it is published."}
          action={
            filtered ? (
              <ButtonLink href={clearHref ?? basePath} variant="outline">
                Clear search and filters
              </ButtonLink>
            ) : undefined
          }
        />
      ) : (
        <CampaignGrid campaigns={page.items} label="Campaigns" headingLevel={2} priorityFirst surface={surface} />
      )}
      {next || query.cursor ? (
        <nav aria-label="Pagination" className="flex flex-wrap gap-3">
          {query.cursor ? (
            <ButtonLink href={discoveryHref(basePath, query, { cursor: "" }, fixed)} variant="ghost">
              Back to the first page
            </ButtonLink>
          ) : null}
          {next ? (
            <ButtonLink href={discoveryHref(basePath, query, { cursor: next }, fixed)} variant="outline">
              Next page
            </ButtonLink>
          ) : null}
        </nav>
      ) : null}
    </div>
  );
}
