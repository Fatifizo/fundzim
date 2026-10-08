import { Badge } from "@/components/ui/badge";
import { ButtonLink } from "@/components/ui/button";
import { Container } from "@/components/ui/container";

export interface StagePlaceholderProps {
  title: string;
  /** Roadmap stage(s) that will deliver this page, e.g. "Stage 4". */
  stage: string;
  description: string;
  planned?: readonly string[];
}

/** Honest "coming soon" page for routes that are not built yet. No forms, no fake functionality. */
export function StagePlaceholder({ title, stage, description, planned }: StagePlaceholderProps) {
  return (
    <Container className="py-16 sm:py-24">
      <div className="mx-auto max-w-2xl">
        <Badge tone="gold">Coming soon — under development ({stage})</Badge>
        <h1 className="mt-4 font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">{title}</h1>
        <p className="mt-4 text-lg text-ink-600">{description}</p>
        {planned && planned.length > 0 ? (
          <>
            <h2 className="mt-8 text-lg font-semibold text-ink-900">What is planned</h2>
            <ul className="mt-2 list-disc space-y-1 pl-6 text-ink-700">
              {planned.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          </>
        ) : null}
        <p className="mt-8 rounded-xl bg-surface-muted p-4 text-ink-700">
          Nothing on this page works yet: there are no accounts, campaigns, donations or payments in this development
          preview.
        </p>
        <div className="mt-8 flex flex-wrap gap-3">
          <ButtonLink href="/">Back to home</ButtonLink>
          <ButtonLink href="/how-it-works" variant="outline">
            How FundZim will work
          </ButtonLink>
        </div>
      </div>
    </Container>
  );
}
