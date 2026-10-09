import type { ReactNode } from "react";

import { AccountContainer } from "@/components/account/account-shell";

import { VerificationNav } from "./verification-nav";

/** Chrome for the verification and organisation areas of the dashboard (side navigation on wide screens). */
export function VerificationShell({ children }: { children: ReactNode }) {
  return (
    <AccountContainer>
      <div className="grid gap-8 md:grid-cols-[14rem_1fr]">
        <VerificationNav />
        <div className="min-w-0">{children}</div>
      </div>
    </AccountContainer>
  );
}
