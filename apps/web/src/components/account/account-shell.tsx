import type { ReactNode } from "react";

import { Container } from "@/components/ui/container";

export function AccountPage({ title, intro, children }: { title: string; intro?: ReactNode; children: ReactNode }) {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">{title}</h1>
        {intro ? <div className="mt-2 text-lg text-ink-600">{intro}</div> : null}
      </div>
      {children}
    </div>
  );
}

export function AccountContainer({ children }: { children: ReactNode }) {
  return <Container className="py-10 sm:py-14">{children}</Container>;
}
