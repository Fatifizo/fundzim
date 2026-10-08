import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { describeSecurityEvent, formatDateTime } from "@/components/account/format";
import { PhoneVerification } from "@/components/account/phone-verification";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { requireUser, serverGet } from "@/lib/auth/session";
import type { SecurityOverview } from "@/lib/auth/types";

export const metadata: Metadata = { title: "Security settings", robots: { index: false, follow: false } };

function Status({ ok, yes, no }: { ok: boolean; yes: string; no: string }) {
  return ok ? <Badge tone="brand">{yes}</Badge> : <Badge tone="gold">{no}</Badge>;
}

export default async function SecuritySettingsPage() {
  const me = await requireUser("/settings/security");
  const security = await serverGet<SecurityOverview>("/api/v1/me/security", "/settings/security");
  return (
    <AccountPage title="Security">
      <Card as="section" aria-labelledby="overview-title">
        <h2 id="overview-title" className="font-display text-xl font-semibold text-ink-900">
          Overview
        </h2>
        <dl className="mt-4 grid gap-3 text-ink-700 sm:grid-cols-2">
          <div>
            <dt className="text-sm font-semibold text-ink-600">Email address</dt>
            <dd><Status ok={security.email_verified} yes="Confirmed" no="Not confirmed" /></dd>
          </div>
          <div>
            <dt className="text-sm font-semibold text-ink-600">Phone number</dt>
            <dd><Status ok={security.phone_verified} yes="Confirmed" no="Not confirmed" /></dd>
          </div>
          <div>
            <dt className="text-sm font-semibold text-ink-600">Two-step verification</dt>
            <dd className="flex flex-wrap items-center gap-2">
              <Status ok={security.mfa_enabled} yes="On" no="Off" />
              <Link href="/settings/mfa" className="font-semibold text-brand-700 underline">
                Manage
              </Link>
            </dd>
          </div>
          <div>
            <dt className="text-sm font-semibold text-ink-600">Password last changed</dt>
            <dd>{formatDateTime(security.password_changed_at)}</dd>
          </div>
        </dl>
      </Card>
      <Card as="section" aria-labelledby="phone-title">
        <h2 id="phone-title" className="mb-4 font-display text-xl font-semibold text-ink-900">
          Phone number
        </h2>
        <PhoneVerification phoneMasked={me.phone_masked} phoneVerified={me.phone_verified} />
      </Card>
      <Card as="section" aria-labelledby="events-title">
        <h2 id="events-title" className="font-display text-xl font-semibold text-ink-900">
          Recent security activity
        </h2>
        {security.recent_events.length === 0 ? (
          <p className="mt-3 text-ink-700">No recent activity.</p>
        ) : (
          <ul className="mt-3 divide-y divide-line">
            {security.recent_events.map((event, index) => (
              <li key={`${event.type}-${event.occurred_at}-${index}`} className="flex flex-wrap justify-between gap-2 py-2 text-ink-700">
                <span>{describeSecurityEvent(event.type)}</span>
                <span className="text-ink-600">{formatDateTime(event.occurred_at)}</span>
              </li>
            ))}
          </ul>
        )}
        <p className="mt-4 text-ink-700">
          Don&apos;t recognise something?{" "}
          <Link href="/settings/sessions" className="font-semibold text-brand-700 underline">
            Review your sessions
          </Link>{" "}
          and change your password.
        </p>
      </Card>
    </AccountPage>
  );
}
