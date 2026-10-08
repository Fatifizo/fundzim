import type { Metadata } from "next";
import Link from "next/link";

import { ContentPage } from "@/components/content-page";
import { Alert } from "@/components/ui/alert";

export const metadata: Metadata = {
  title: "How it works",
  description: "How FundZim is being designed to work for fundraisers and donors.",
};

export default function HowItWorksPage() {
  return (
    <ContentPage
      title="How FundZim will work"
      intro="FundZim is a donation-based crowdfunding platform being built for Zimbabwe. This page describes the planned design; none of it is live yet."
      notice={
        <Alert tone="notice" title="Not open yet">
          You cannot create a campaign or make a donation today. Everything below is planned and may change as the
          platform is built and reviewed.
        </Alert>
      }
    >
      <h2>For fundraisers</h2>
      <ol>
        <li>
          <strong>Create a campaign.</strong> Explain who the money is for and why: medical bills, school fees, a
          funeral, a community project or another approved cause.
        </li>
        <li>
          <strong>Verify who you are.</strong> Campaign owners and organisations will complete identity checks before
          they can receive funds.
        </li>
        <li>
          <strong>Get reviewed.</strong> Every campaign will be reviewed before it is published.
        </li>
        <li>
          <strong>Share your story.</strong> Share your link on WhatsApp and social media. Pages are designed to load
          quickly on mobile phones.
        </li>
        <li>
          <strong>Receive support.</strong> Payouts will go to the verified beneficiary after checks are complete.
        </li>
      </ol>

      <h2>For donors</h2>
      <ul>
        <li>Give from Zimbabwe or abroad with payment methods that suit you, such as mobile money, local bank rails or an international card.</li>
        <li>Choose whether your name is shown publicly on a campaign.</li>
        <li>Report any campaign you are worried about.</li>
      </ul>

      <h2>Where the money goes</h2>
      <p>
        FundZim is being designed so that donations are collected and paid out by licensed payment service providers,
        while FundZim manages campaigns, keeps records and coordinates payouts. The exact arrangement is subject to
        legal review and provider agreements, and no provider has been selected yet.
      </p>
      <p>
        US dollars and ZiG will be kept separate: a campaign shows what it raised in each currency, and FundZim will
        not convert between them.
      </p>

      <h2>What FundZim is not</h2>
      <p>
        FundZim is for donations only. It is not an investment, lending, rewards or wallet product, and donors receive
        nothing of financial value in return.
      </p>
      <p>
        Questions? See <Link href="/about">About FundZim</Link>.
      </p>
    </ContentPage>
  );
}
