import type { Metadata } from "next";

import { AccountContainer, AccountPage } from "@/components/account/account-shell";
import { StaffLinkConfirm } from "@/components/campaigns/staff-link-confirm";
import { requireUser } from "@/lib/auth/session";
import { first } from "@/lib/campaigns/paths";

export const metadata: Metadata = {
  title: "Link your staff account",
  robots: { index: false, follow: false, nocache: true },
  referrer: "no-referrer",
};

const PATH = "/dashboard/account/staff-link";
const TOKEN = /^[A-Za-z0-9_-]{16,256}$/;

/**
 * The personal account confirms a staff link (ADR-037 §4). The token travels in the query string only as
 * far as this page (no-referrer, never logged by this app) and is sent in a POST body on confirmation.
 */
export default async function StaffLinkPage({ searchParams }: PageProps<"/dashboard/account/staff-link">) {
  const raw = first((await searchParams).token);
  const token = raw && TOKEN.test(raw) ? raw : null;
  await requireUser(token ? `${PATH}?token=${encodeURIComponent(token)}` : PATH);
  return (
    <AccountContainer>
      <AccountPage title="Link your staff account">
        <StaffLinkConfirm token={token} />
      </AccountPage>
    </AccountContainer>
  );
}
