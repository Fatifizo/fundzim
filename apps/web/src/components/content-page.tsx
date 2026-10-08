import type { ReactNode } from "react";

import { Container } from "@/components/ui/container";

/** Layout for static text pages (about, how it works, legal drafts). */
export function ContentPage({ title, intro, notice, children }: { title: string; intro?: ReactNode; notice?: ReactNode; children?: ReactNode }) {
  return (
    <Container className="py-12 sm:py-16">
      <article className="mx-auto max-w-3xl">
        <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">{title}</h1>
        {intro ? <div className="mt-4 text-lg text-ink-600">{intro}</div> : null}
        {notice ? <div className="mt-6">{notice}</div> : null}
        <div className="mt-8 space-y-4 text-ink-700 [&_a]:font-medium [&_a]:text-brand-700 [&_a]:underline [&_h2]:mt-10 [&_h2]:font-display [&_h2]:text-2xl [&_h2]:font-semibold [&_h2]:text-ink-900 [&_li]:mt-1 [&_ol]:list-decimal [&_ol]:pl-6 [&_ul]:list-disc [&_ul]:pl-6">
          {children}
        </div>
      </article>
    </Container>
  );
}
