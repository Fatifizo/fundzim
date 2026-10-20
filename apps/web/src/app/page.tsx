import type { Metadata } from "next";
import Link from "next/link";

import { CampaignGrid } from "@/components/campaigns/campaign-card";
import { liveCountText } from "@/components/campaigns/category-card";
import { JsonLdScript } from "@/components/seo/json-ld";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Container } from "@/components/ui/container";
import { EmptyState } from "@/components/ui/empty-state";
import { SectionHeading } from "@/components/ui/section-heading";
import { fetchFeatured, fetchPublicCategories } from "@/lib/campaigns/public";
import { organizationJsonLd } from "@/lib/seo/json-ld";
import { pageMetadata } from "@/lib/seo/metadata";
import { siteOrigin } from "@/lib/site/url";

const HERO_TITLE = "Together, We Can Make a Difference.";
const HERO_SUBTITLE = "Support people and causes across Zimbabwe through simple, transparent fundraising.";

export function generateMetadata(): Metadata {
  return {
    ...pageMetadata({ title: "FundZim — Fund anyone in Zimbabwe, from anywhere", description: HERO_SUBTITLE, path: "/" }),
    title: { absolute: "FundZim — Fund anyone in Zimbabwe, from anywhere" },
  };
}

const STEPS = [
  { title: "Create your campaign", body: "Explain who the money is for and why, set a goal in one currency, and add a cover photo." },
  { title: "Get reviewed", body: "Organisers verify who they are, and a FundZim reviewer checks every campaign before it can be published." },
  { title: "Share your story", body: "Share your campaign link on WhatsApp, social media or by email. Pages load quickly on mobile phones." },
  { title: "Receive support when payments become available", body: "Donations are not available yet. When payments open, they will go through licensed payment providers, never through FundZim staff or personal accounts." },
] as const;

const TRUST = [
  { title: "Reviewed before publishing", body: "Every campaign is checked by a FundZim reviewer against our rules before anyone can see it. Changes to a published campaign are reviewed again." },
  { title: "Verification shown honestly", body: "When an organiser, organisation or beneficiary has completed a FundZim check, the campaign page says so — and only then." },
  { title: "Report anything that worries you", body: "Every campaign page has a Report button. Reports go to the review team, who decide what to do." },
  { title: "What checks cannot promise", body: "A verified identity does not guarantee that every claim in a story is true. Read carefully, and never send money outside FundZim." },
] as const;

/** Home: hero, featured campaigns, categories, how it works, trust and safety, call to action. */
export default async function HomePage() {
  const [featured, categories] = await Promise.all([fetchFeatured(6), fetchPublicCategories()]);
  const origin = siteOrigin();
  const hasPlacements = featured.ok && featured.data.some((c) => c.selection === "FEATURED");

  return (
    <>
      {origin ? <JsonLdScript data={organizationJsonLd(origin)} /> : null}
      <section aria-labelledby="hero-title" className="on-dark bg-brand-900 text-white">
        <Container className="py-14 sm:py-24">
          <p className="font-semibold text-gold-400">Fund anyone in Zimbabwe, from anywhere.</p>
          <h1 id="hero-title" className="mt-3 max-w-3xl font-display text-4xl font-bold tracking-tight break-words sm:text-5xl lg:text-6xl">
            {HERO_TITLE}
          </h1>
          <p className="mt-5 max-w-2xl text-lg text-brand-100 sm:text-xl">{HERO_SUBTITLE}</p>
          <div className="mt-8 flex flex-col gap-3 sm:flex-row">
            <ButtonLink href="/campaigns" variant="secondary" size="lg">
              Explore Campaigns
            </ButtonLink>
            <ButtonLink href="/dashboard/campaigns/new" size="lg" variant="inverse">
              Start a Fundraiser
            </ButtonLink>
          </div>
          <p className="mt-6 max-w-2xl text-sm text-brand-100">Donations are not available yet: you can read and share campaigns, but no payment can be made on FundZim today.</p>
        </Container>
      </section>

      <section aria-labelledby="featured-title" className="py-14 sm:py-20">
        <Container>
          <SectionHeading
            id="featured-title"
            eyebrow="Featured campaigns"
            title="Causes you can support"
            description={
              hasPlacements
                ? "Some campaigns are featured by the FundZim team for a set period (a second team member must agree to each choice); the rest are the most recently published. Being featured is not an endorsement or a ranking, and nobody can pay for it."
                : "The most recently published campaigns. Being shown here is not an endorsement or a ranking."
            }
          />
          <div className="mt-8">
            {!featured.ok ? (
              <EmptyState
                title="Featured campaigns could not be loaded"
                description="Please try again in a moment, or browse every campaign."
                action={
                  <ButtonLink href="/campaigns" variant="outline">
                    Browse campaigns
                  </ButtonLink>
                }
              />
            ) : featured.data.length === 0 ? (
              <EmptyState
                title="No campaigns are published yet"
                description="Campaigns appear here once a reviewer has approved them and their organisers publish them."
                action={<ButtonLink href="/dashboard/campaigns/new">Start a Fundraiser</ButtonLink>}
              />
            ) : (
              <>
                <CampaignGrid campaigns={featured.data} label="Featured campaigns" />
                <div className="mt-8">
                  <ButtonLink href="/campaigns" variant="outline">
                    See all campaigns
                  </ButtonLink>
                </div>
              </>
            )}
          </div>
        </Container>
      </section>

      <section aria-labelledby="categories-title" className="bg-surface-muted py-14 sm:py-20">
        <Container>
          <SectionHeading id="categories-title" eyebrow="Categories" title="Find a cause" />
          {!categories.ok ? (
            <p className="mt-6 text-ink-700">
              Categories could not be loaded right now. <Link href="/campaigns" className="font-semibold text-brand-700 underline">Browse all campaigns</Link> instead.
            </p>
          ) : categories.data.length === 0 ? (
            <p className="mt-6 text-ink-700">Categories will appear here once they are set up.</p>
          ) : (
            <ul aria-label="Categories" className="mt-8 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {categories.data.map((c) => (
                <li key={c.code}>
                  <Link href={`/categories/${c.slug}`} className="flex min-h-14 items-center justify-between gap-3 rounded-xl border border-line bg-surface px-4 py-3 font-semibold text-ink-900 hover:border-brand-700 hover:bg-brand-50">
                    <span className="break-words">{c.name}</span>
                    {c.live_campaigns !== null ? <span className="shrink-0 text-sm font-normal text-ink-600">{liveCountText(c.live_campaigns)}</span> : null}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Container>
      </section>

      <section aria-labelledby="how-title" className="py-14 sm:py-20">
        <Container>
          <SectionHeading id="how-title" eyebrow="How it works" title="Four steps" />
          <ol className="mt-8 grid gap-6 md:grid-cols-2 lg:grid-cols-4">
            {STEPS.map((step, index) => (
              <Card as="li" key={step.title}>
                <span aria-hidden="true" className="inline-flex size-10 items-center justify-center rounded-full bg-gold-400 font-display text-lg font-bold text-ink-900">
                  {index + 1}
                </span>
                <h3 className="mt-4 text-xl font-semibold text-ink-900">
                  <span className="sr-only">{`Step ${index + 1}:`}</span> {step.title}
                </h3>
                <p className="mt-2 text-ink-600">{step.body}</p>
              </Card>
            ))}
          </ol>
          <div className="mt-8">
            <ButtonLink href="/how-it-works" variant="ghost">
              More about how FundZim works
            </ButtonLink>
          </div>
        </Container>
      </section>

      <section aria-labelledby="trust-title" className="bg-brand-50 py-14 sm:py-20">
        <Container>
          <SectionHeading id="trust-title" eyebrow="Trust and safety" title="How FundZim looks after donors and beneficiaries" />
          <ul className="mt-8 grid gap-6 md:grid-cols-2">
            {TRUST.map((item) => (
              <Card as="li" key={item.title}>
                <h3 className="text-xl font-semibold text-ink-900">{item.title}</h3>
                <p className="mt-2 text-ink-600">{item.body}</p>
              </Card>
            ))}
          </ul>
          <div className="mt-8">
            <ButtonLink href="/safety" variant="outline">
              Read our safety guidance
            </ButtonLink>
          </div>
        </Container>
      </section>

      <section aria-labelledby="cta-title" className="on-dark bg-brand-900 py-14 text-white sm:py-20">
        <Container className="flex flex-col gap-6 md:flex-row md:items-center md:justify-between">
          <div className="max-w-2xl">
            <h2 id="cta-title" className="font-display text-3xl font-bold tracking-tight">
              Raising money for someone you care about?
            </h2>
            <p className="mt-3 text-lg text-brand-100">Prepare your campaign now. It will be reviewed before it is published, and donations will open when payments become available.</p>
          </div>
          <div className="flex flex-col gap-3 sm:flex-row">
            <ButtonLink href="/dashboard/campaigns/new" variant="secondary" size="lg">
              Start a Fundraiser
            </ButtonLink>
            <ButtonLink href="/how-it-works" variant="inverse" size="lg">
              How It Works
            </ButtonLink>
          </div>
        </Container>
      </section>
    </>
  );
}
