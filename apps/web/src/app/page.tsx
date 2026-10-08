import { CampaignCard } from "@/components/campaign-card";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Container } from "@/components/ui/container";
import { EmptyState } from "@/components/ui/empty-state";
import { SectionHeading } from "@/components/ui/section-heading";

const STEPS = [
  {
    title: "Create a campaign",
    body: "Tell us who you are raising money for and why. Every campaign will be reviewed before it goes public.",
  },
  {
    title: "Share your story",
    body: "Share your campaign link with family, friends and community — on WhatsApp, social media or by word of mouth.",
  },
  {
    title: "Receive support",
    body: "Supporters in Zimbabwe and abroad will give using payment methods that suit them, and funds will go to the verified beneficiary.",
  },
] as const;

const PAYMENT_METHODS = [
  { title: "Mobile money", body: "For example EcoCash, OneMoney, InnBucks and O'Mari." },
  { title: "Local bank rails", body: "ZimSwitch-connected bank payments." },
  { title: "International cards", body: "So family and friends in the diaspora can give from anywhere." },
] as const;

const TRUST = [
  {
    title: "Identity verification",
    body: "Campaign owners and organisations will verify who they are before they can receive funds.",
  },
  {
    title: "Transparent transactions",
    body: "Every donation and payout will be recorded and traceable end to end, kept separately per currency (USD and ZiG).",
  },
  {
    title: "Beneficiary protection",
    body: "Campaigns will be reviewed, can be reported by anyone, and payouts will be checked before funds are released.",
  },
] as const;

export default function HomePage() {
  return (
    <>
      <section aria-labelledby="hero-title" className="on-dark bg-brand-900 text-white">
        <Container className="py-16 sm:py-24">
          <p className="font-semibold text-gold-400">Fund anyone in Zimbabwe, from anywhere.</p>
          <h1 id="hero-title" className="mt-3 max-w-3xl font-display text-4xl font-bold tracking-tight sm:text-5xl lg:text-6xl">
            Together, We Can Make a Difference.
          </h1>
          <p className="mt-5 max-w-2xl text-lg text-brand-100 sm:text-xl">
            Support people and causes across Zimbabwe through simple, secure fundraising.
          </p>
          <div className="mt-8 flex flex-col gap-3 sm:flex-row">
            <ButtonLink href="/start" variant="secondary" size="lg">
              Start a Fundraiser
            </ButtonLink>
            <ButtonLink href="/explore" size="lg" variant="inverse">
              Explore Campaigns
            </ButtonLink>
          </div>
          <p className="mt-6 max-w-2xl text-sm text-brand-100">
            FundZim is in development. Fundraising and donations are not open yet; these pages show what is being built.
          </p>
        </Container>
      </section>

      <section aria-labelledby="how-title" className="py-16 sm:py-20">
        <Container>
          <SectionHeading id="how-title" eyebrow="How it works" title="Three simple steps" />
          <ol className="mt-10 grid gap-6 md:grid-cols-3">
            {STEPS.map((step, index) => (
              <Card as="li" key={step.title}>
                <span
                  aria-hidden="true"
                  className="inline-flex size-10 items-center justify-center rounded-full bg-gold-400 font-display text-lg font-bold text-ink-900"
                >
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
              Learn more about how FundZim will work
            </ButtonLink>
          </div>
        </Container>
      </section>

      <section aria-labelledby="payments-title" className="bg-surface-muted py-16 sm:py-20">
        <Container>
          <SectionHeading
            id="payments-title"
            eyebrow="Payment methods"
            title="Designed for the way Zimbabwe pays"
            description="FundZim is being designed for Zimbabwean payment methods, processed through licensed payment service providers."
          />
          <ul className="mt-10 grid gap-6 md:grid-cols-3">
            {PAYMENT_METHODS.map((method) => (
              <Card as="li" key={method.title}>
                <h3 className="text-xl font-semibold text-ink-900">{method.title}</h3>
                <p className="mt-2 text-ink-600">{method.body}</p>
              </Card>
            ))}
          </ul>
          <Alert tone="notice" title="Payments are not live yet" className="mt-8">
            No payment provider has been selected and no payment can be made on FundZim today. Which methods are offered
            will depend on agreements with licensed providers and on legal review. Names above describe payment types
            commonly used in Zimbabwe; they do not indicate any partnership.
          </Alert>
        </Container>
      </section>

      <section aria-labelledby="featured-title" className="py-16 sm:py-20">
        <Container>
          <SectionHeading id="featured-title" eyebrow="Featured campaigns" title="Causes you can support" />
          <EmptyState
            className="mt-10"
            title="Campaigns will appear here once fundraising opens"
            description="There are no live campaigns yet. Every campaign will be reviewed before it is published."
          />
          <div className="mt-10 grid gap-6 md:grid-cols-2 lg:grid-cols-3">
            <CampaignCard
              preview
              title="Example: Borehole for a community school"
              summary="This card shows how a campaign will be presented. It is a design example, not a real appeal."
              category="Community"
              location="Example location, Zimbabwe"
            />
          </div>
        </Container>
      </section>

      <section aria-labelledby="trust-title" className="bg-brand-50 py-16 sm:py-20">
        <Container>
          <SectionHeading
            id="trust-title"
            eyebrow="Trust"
            title="Built for trust from day one"
            description="These protections are planned and being built now. None of them is active yet."
          />
          <ul className="mt-10 grid gap-6 md:grid-cols-3">
            {TRUST.map((item) => (
              <Card as="li" key={item.title}>
                <Badge tone="brand">Planned — being built</Badge>
                <h3 className="mt-4 text-xl font-semibold text-ink-900">{item.title}</h3>
                <p className="mt-2 text-ink-600">{item.body}</p>
              </Card>
            ))}
          </ul>
        </Container>
      </section>
    </>
  );
}
