import { permanentRedirect } from "next/navigation";

import { discoveryHref, readDiscoveryQuery } from "@/lib/discovery/query";

/** /campaigns/search is an alias of the directory (one search page, one canonical URL). Validated parameters only. */
export default async function SearchAlias({ searchParams }: PageProps<"/campaigns/search">) {
  permanentRedirect(discoveryHref("/campaigns", readDiscoveryQuery(await searchParams)));
}
