import { AccountPage } from "@/components/account/account-shell";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { normaliseQueue } from "@/lib/verification/normalise";
import type { ReviewCaseType } from "@/lib/verification/types";

import { parseQueueFilters, queueApiPath, ReviewQueue } from "./review-queue";

/** Server-rendered queue page; non-staff and staff without `kyc.case.review` get a 404. */
export async function ReviewQueuePage({
  basePath,
  title,
  fixedType,
  searchParams,
}: {
  basePath: string;
  title: string;
  fixedType?: ReviewCaseType;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const me = await requireStaff();
  const filters = parseQueueFilters(await searchParams, fixedType);
  const result = await serverGetOr404<unknown>(queueApiPath(filters), basePath);
  const { items, nextCursor } = normaliseQueue(result.data, result.meta.next_cursor, me.id);
  return (
    <AccountPage title={title} intro="Cases waiting for review. Open a case to assign it, review the evidence and record a decision.">
      <ReviewQueue basePath={basePath} filters={filters} fixedType={fixedType} cases={items} nextCursor={nextCursor} />
    </AccountPage>
  );
}
