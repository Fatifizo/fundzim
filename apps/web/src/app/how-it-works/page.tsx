import type { Metadata } from "next";
import Link from "next/link";

import { ContentPage } from "@/components/content-page";
import { Alert } from "@/components/ui/alert";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({
    title: "How it works",
    description: "How FundZim works for fundraisers and supporters: create, get reviewed, share, and receive support when payments become available.",
    path: "/how-it-works",
  });
}

export default function HowItWorksPage() {
  return (
    <ContentPage
      title="How FundZim works"
      intro="FundZim is a donation-based crowdfunding platform being built for Zimbabwe. Campaigns can already be created, reviewed, published and shared. Donations are not available yet."
      notice={
        <Alert tone="notice" title="Donations are not available yet">
          You can read and share campaigns today, but no payment can be made on FundZim. Please do not send money to anyone
          because of a FundZim campaign page.
        </Alert>
      }
    >
      <h2>For fundraisers</h2>
      <ol>
        <li>
          <strong>Create your campaign.</strong> Explain who the money is for and why — medical bills, school fees, a
          funeral, a community project or another allowed cause — and set a goal in one currency.
        </li>
        <li>
          <strong>Verify who you are.</strong> Organisers confirm their identity, and organisations complete an
          organisation check, before a campaign can be submitted.
        </li>
        <li>
          <strong>Get reviewed.</strong> A FundZim reviewer checks every campaign before it can be published. Material
          changes to a published campaign are reviewed again.
        </li>
        <li>
          <strong>Share your story.</strong> Share your campaign link on WhatsApp, Facebook, X or by email. Pages are
          designed to load quickly on mobile phones.
        </li>
        <li>
          <strong>Receive support when payments become available.</strong> Donations are not open yet. When they open,
          payouts will go to the verified beneficiary after checks are complete.
        </li>
      </ol>

      <h2>For supporters</h2>
      <ul>
        <li>Browse <Link href="/campaigns">campaigns</Link> or <Link href="/categories">categories</Link>, and read each story and its updates.</li>
        <li>Each campaign page shows which checks were completed — and explains what they do not guarantee.</li>
        <li>Share campaigns you care about, and report any campaign that worries you.</li>
      </ul>

      <h2>Where the money will go</h2>
      <p>
        FundZim is being designed so that donations are collected and paid out by licensed payment service providers,
        while FundZim manages campaigns, keeps records and coordinates payouts. The exact arrangement is subject to legal
        review and provider agreements, and no provider has been selected yet.
      </p>
      <p>
        US dollars and ZiG will be kept separate, and FundZim will not convert between them. Campaign pages show the goal
        only: there are no totals or progress bars because no donations exist yet.
      </p>

      <h2>What FundZim is not</h2>
      <p>
        FundZim is for donations only. It is not an investment, lending, rewards or wallet product, and donors receive
        nothing of financial value in return.
      </p>
      <p>
        Read more about <Link href="/safety">safety on FundZim</Link> or <Link href="/about">about FundZim</Link>.
      </p>
    </ContentPage>
  );
}
