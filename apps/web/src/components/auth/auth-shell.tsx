import type { ReactNode } from "react";

import { Container } from "@/components/ui/container";

/** Narrow, centred layout for the sign-in/registration pages. */
export function AuthShell({ title, intro, children, footer }: { title: string; intro?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  return (
    <Container className="py-12 sm:py-16">
      <div className="mx-auto max-w-lg">
        <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">{title}</h1>
        {intro ? <div className="mt-3 text-lg text-ink-600">{intro}</div> : null}
        <div className="mt-8 rounded-card border border-line bg-surface p-6 shadow-card sm:p-8">{children}</div>
        {footer ? <div className="mt-6 text-ink-700">{footer}</div> : null}
      </div>
    </Container>
  );
}

/** Notice shown on every auth page while FundZim is a development preview. */
export function PreviewAccountNotice() {
  return (
    <p className="text-sm text-ink-600">
      FundZim is a development preview. Accounts exist so the platform can be tested; no fundraising, donations or
      payments are possible yet.
    </p>
  );
}
