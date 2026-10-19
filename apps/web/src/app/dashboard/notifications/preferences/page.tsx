import type { Metadata } from "next";

import { AccountContainer, AccountPage } from "@/components/account/account-shell";
import { Section } from "@/components/admin/payments/common";
import { ConsentPanel, PreferencesForm } from "@/components/notifications/preferences";
import { requireUser, serverGet } from "@/lib/auth/session";
import { items } from "@/lib/operations/normalise";
import type { Consent, Preference } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Notification preferences", robots: { index: false, follow: false, nocache: true } };

const BASE = "/dashboard/notifications/preferences";

/** Notification preferences and communication consent (the person's own; changes are audited). */
export default async function NotificationPreferencesPage() {
  await requireUser(BASE);
  const [prefs, consents] = await Promise.all([serverGet<unknown>("/api/v1/notifications/preferences", BASE), serverGet<unknown>("/api/v1/communications/consent", BASE)]);
  return (
    <AccountContainer>
      <AccountPage title="Notification preferences" intro="Choose how FundZim tells you about your account and activity. In-app notices are always on.">
        <Section id="preferences" title="Channels">
          <PreferencesForm items={items<Preference>(prefs)} />
        </Section>
        <Section id="consent" title="Consent for SMS and WhatsApp" description="SMS and WhatsApp messages need your consent as well as the channel being turned on.">
          <ConsentPanel items={items<Consent>(consents)} />
        </Section>
      </AccountPage>
    </AccountContainer>
  );
}
