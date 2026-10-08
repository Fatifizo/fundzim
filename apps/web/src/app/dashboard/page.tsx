import type { Metadata } from "next";

import { AccountContainer, AccountPage } from "@/components/account/account-shell";
import { EmailVerificationBanner } from "@/components/account/email-verification-banner";
import { formatDateTime } from "@/components/account/format";
import { Badge } from "@/components/ui/badge";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { requireUser } from "@/lib/auth/session";

export const metadata: Metadata = {
  title: "Your dashboard",
  robots: { index: false, follow: false },
};

export default async function DashboardPage() {
  const me = await requireUser("/dashboard");
  return (
    <AccountContainer>
      <AccountPage title={`Welcome, ${me.display_name || "there"}`} intro="Your FundZim account.">
        {!me.email_verified ? <EmailVerificationBanner email={me.email} /> : null}
        <div className="grid gap-6 md:grid-cols-2">
          <Card as="section" aria-labelledby="account-title">
            <h2 id="account-title" className="font-display text-xl font-semibold text-ink-900">
              Account
            </h2>
            <dl className="mt-4 space-y-3 text-ink-700">
              <div>
                <dt className="text-sm font-semibold text-ink-600">Display name</dt>
                <dd className="break-words">{me.display_name}</dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Email</dt>
                <dd className="flex flex-wrap items-center gap-2 break-all">
                  {me.email}
                  {me.email_verified ? <Badge tone="brand">Confirmed</Badge> : <Badge tone="gold">Not confirmed</Badge>}
                </dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Phone</dt>
                <dd>
                  {me.phone_masked ?? "Not added"}
                  {me.phone_masked ? (me.phone_verified ? " (confirmed)" : " (not confirmed)") : null}
                </dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Member since</dt>
                <dd>{formatDateTime(me.created_at)}</dd>
              </div>
            </dl>
            <ButtonLink href="/settings/profile" variant="outline" className="mt-5">
              Edit profile
            </ButtonLink>
          </Card>
          <Card as="section" aria-labelledby="security-title">
            <h2 id="security-title" className="font-display text-xl font-semibold text-ink-900">
              Security
            </h2>
            <p className="mt-4 text-ink-700">
              Two-step verification: {me.mfa_enabled ? <Badge tone="brand">On</Badge> : <Badge tone="neutral">Off</Badge>}
            </p>
            {!me.mfa_enabled ? (
              <p className="mt-2 text-ink-700">Add an authenticator app so a stolen password is not enough to get into your account.</p>
            ) : null}
            <div className="mt-5 flex flex-wrap gap-3">
              <ButtonLink href="/settings/security" variant="outline">
                Security settings
              </ButtonLink>
              <ButtonLink href="/settings/sessions" variant="ghost">
                Your sessions
              </ButtonLink>
            </div>
          </Card>
        </div>
        <Card as="section" aria-labelledby="fundraising-title">
          <h2 id="fundraising-title" className="font-display text-xl font-semibold text-ink-900">
            Fundraising
          </h2>
          <p className="mt-2 text-ink-700">
            Campaigns, donations and payouts are not available yet in this development preview.
          </p>
        </Card>
      </AccountPage>
    </AccountContainer>
  );
}
