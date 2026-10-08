import type { Metadata } from "next";

import { AccountContainer } from "@/components/account/account-shell";
import { SettingsNav } from "@/components/account/settings-nav";
import { StepUpProvider } from "@/components/auth/step-up";
import { getSession } from "@/lib/auth/session";

export const metadata: Metadata = {
  robots: { index: false, follow: false },
};

/**
 * Settings area chrome. Access control is done by EVERY page (`requireUser` with its own path, so the
 * login redirect returns to the right place); layouts are not re-run on every client navigation and must
 * not be the only check. Without a session the layout renders bare children, and the page redirects.
 */
export default async function SettingsLayout({ children }: LayoutProps<"/settings">) {
  const session = await getSession();
  if (!session.authenticated || !session.user) return <>{children}</>;
  const me = session.user;
  return (
    <AccountContainer>
      <div className="grid gap-8 md:grid-cols-[14rem_1fr]">
        <SettingsNav />
        <StepUpProvider mfaEnabled={me.mfa_enabled} codeOnly={me.account_kind === "STAFF" || (me.roles?.length ?? 0) > 0}>
          <div className="min-w-0">{children}</div>
        </StepUpProvider>
      </div>
    </AccountContainer>
  );
}
