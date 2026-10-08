import type { Metadata } from "next";

import { ButtonLink } from "@/components/ui/button";
import { Container } from "@/components/ui/container";

export const metadata: Metadata = { title: "Page not found" };

export default function NotFound() {
  return (
    <Container className="py-24">
      <div className="mx-auto max-w-xl text-center">
        <p className="font-semibold text-brand-700">404</p>
        <h1 className="mt-2 font-display text-3xl font-bold text-ink-900 sm:text-4xl">We couldn&apos;t find that page</h1>
        <p className="mt-4 text-lg text-ink-600">
          The link may be mistyped, or the page may not exist yet. FundZim is still under development.
        </p>
        <div className="mt-8 flex flex-wrap justify-center gap-3">
          <ButtonLink href="/">Go to the homepage</ButtonLink>
          <ButtonLink href="/how-it-works" variant="outline">
            How it works
          </ButtonLink>
        </div>
      </div>
    </Container>
  );
}
