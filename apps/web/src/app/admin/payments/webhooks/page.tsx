import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, InboxTable, NextPage, PAYMENTS_BASE, RejectionsTable, Section, toOptions } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor } from "@/lib/campaigns/normalise";
import { isCursor } from "@/lib/campaigns/paths";
import { buildQuery, cursorParam, pick } from "@/lib/payments/labels";
import { readInboxItems, readRejections } from "@/lib/payments/normalise";
import { INBOX_STATUSES } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Webhooks", robots: { index: false, follow: false, nocache: true } };

const BASE = `${PAYMENTS_BASE}/webhooks`;

/**
 * Provider webhook inbox and rejections (`payment.view`). The inbox lists verified, stored webhooks; rejections are
 * requests that failed verification (no payload is stored for them). The two lists page independently
 * (`cursor` / `rejections_cursor`).
 */
export default async function WebhooksPage({ searchParams }: PageProps<"/admin/payments/webhooks">) {
  await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, INBOX_STATUSES);
  const cursor = cursorParam(p.cursor);
  const rejectionsCursor = cursorParam(p.rejections_cursor);
  const [inbox, rejections] = await Promise.all([
    serverGetOr404<unknown>(`/api/v1/admin/payments/webhooks${buildQuery({ status, cursor })}`, BASE),
    serverGetOr404<unknown>(`/api/v1/admin/payments/webhook-rejections${buildQuery({ cursor: rejectionsCursor })}`, BASE),
  ]);
  const items = readInboxItems(inbox.data);
  const next = readNextCursor(inbox.data, inbox.meta);
  const rejected = readRejections(rejections.data);
  const nextRejections = readNextCursor(rejections.data, rejections.meta);
  return (
    <AccountPage title="Webhooks" intro="Verified provider webhooks, newest first. Acknowledged means stored, not necessarily applied.">
      <FilterForm action={BASE} label="Filter webhooks" fields={[{ name: "status", label: "Status", value: status, options: toOptions(INBOX_STATUSES) }]} />
      <InboxTable items={items} caption="Webhook inbox" />
      <NextPage base={BASE} query={{ status, rejections_cursor: rejectionsCursor }} next={next && isCursor(next) ? next : null} />
      <Section id="rejections-title" title="Rejected requests" description="Requests refused before storage (unknown provider, bad signature or timestamp, malformed…). Only a hash of the body is kept.">
        <RejectionsTable items={rejected} />
        <NextPage
          base={BASE}
          query={{ status, cursor }}
          cursorName="rejections_cursor"
          next={nextRejections && isCursor(nextRejections) ? nextRejections : null}
          label="Next page of rejections"
        />
      </Section>
    </AccountPage>
  );
}
