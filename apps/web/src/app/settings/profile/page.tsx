import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { ChangePasswordForm } from "@/components/account/change-password-form";
import { ProfileForm } from "@/components/account/profile-form";
import { Card } from "@/components/ui/card";
import { requireUser } from "@/lib/auth/session";

export const metadata: Metadata = { title: "Profile settings", robots: { index: false, follow: false } };

export default async function ProfileSettingsPage() {
  const me = await requireUser("/settings/profile");
  return (
    <AccountPage title="Profile" intro={<>Signed in as <strong className="break-all">{me.email}</strong>.</>}>
      <Card as="section" aria-labelledby="profile-title">
        <h2 id="profile-title" className="mb-4 font-display text-xl font-semibold text-ink-900">
          Display name
        </h2>
        <ProfileForm displayName={me.display_name} />
      </Card>
      <Card as="section" aria-labelledby="password-title">
        <h2 id="password-title" className="mb-4 font-display text-xl font-semibold text-ink-900">
          Change password
        </h2>
        <ChangePasswordForm email={me.email} />
      </Card>
    </AccountPage>
  );
}
