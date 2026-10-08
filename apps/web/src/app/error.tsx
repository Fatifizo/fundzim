"use client";

import { useEffect } from "react";

import { Button, ButtonLink } from "@/components/ui/button";
import { Container } from "@/components/ui/container";
import { ErrorState } from "@/components/ui/error-state";

export default function RouteError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  useEffect(() => {
    // Production builds only expose a digest for server errors (no internals). Log for the console.
    console.error(error);
  }, [error]);

  return (
    <Container className="py-24">
      <div className="mx-auto max-w-xl">
        <h1 className="sr-only">Error</h1>
        <ErrorState
          title="This page could not be loaded"
          description="Something went wrong on our side. You can try again, or go back to the homepage."
          reference={error.digest}
          action={
            <div className="flex flex-wrap gap-3">
              <Button onClick={() => retry()}>Try again</Button>
              <ButtonLink href="/" variant="outline">
                Go to the homepage
              </ButtonLink>
            </div>
          }
        />
      </div>
    </Container>
  );
}
