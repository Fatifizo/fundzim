import type { Metadata } from "next";
import Link from "next/link";

import { ContentPage } from "@/components/content-page";
import { Alert } from "@/components/ui/alert";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({
    title: "Safety",
    description: "How FundZim reviews campaigns, what verification means and does not guarantee, how to spot suspicious requests and how to report a campaign.",
    path: "/safety",
  });
}

export default function SafetyPage() {
  return (
    <ContentPage
      title="Safety on FundZim"
      intro="How campaigns are checked, what our checks can and cannot tell you, and what to do if something seems wrong."
      notice={
        <Alert tone="notice" title="Donations are not available yet">
          No payment can be made on FundZim today. Anyone asking you to pay for a FundZim campaign — by mobile money, bank
          transfer, card or cash — is not doing it through FundZim.
        </Alert>
      }
    >
      <h2>How review works</h2>
      <ul>
        <li>Every campaign is checked by a FundZim reviewer against our rules before it can be published.</li>
        <li>Higher-risk categories need more checks, and some decisions need two different reviewers.</li>
        <li>When an organiser changes the story, goal, beneficiary or photos of a published campaign, the change is reviewed again.</li>
        <li>Photos are scanned and re-processed before they are shown. Photos of children are not accepted for now.</li>
        <li>FundZim staff can pause or suspend a campaign. A suspended campaign disappears from the site straight away.</li>
      </ul>

      <h2>What verification means</h2>
      <p>A campaign page lists a check only when it is true right now:</p>
      <ul>
        <li><strong>Reviewed before publishing</strong> — a reviewer approved this version of the campaign.</li>
        <li><strong>Organiser&apos;s identity verified</strong> — the organiser completed FundZim&apos;s identity check.</li>
        <li><strong>Organisation verified</strong> — the organisation completed FundZim&apos;s organisation check.</li>
        <li><strong>Beneficiary verified</strong> — the person or organisation the money is for completed FundZim&apos;s beneficiary check.</li>
      </ul>

      <h2>What verification does not guarantee</h2>
      <p>
        Verified identity does not guarantee the campaign&apos;s claims. Checks confirm who people are and that a campaign
        follows our rules; they cannot prove that every statement in a story is true, or that circumstances have not
        changed since the campaign was published. FundZim does not endorse campaigns, and being featured is not a
        recommendation.
      </p>

      <h2>Spotting suspicious requests</h2>
      <ul>
        <li>FundZim will never ask you to send money to a personal phone number, bank account or agent.</li>
        <li>Be careful with urgent messages that pressure you to pay immediately, or that ask you to keep a request secret.</li>
        <li>Check that a link really goes to the FundZim site before you trust it, and compare the campaign with what you know.</li>
        <li>Never share passwords, one-time codes or card details by message. FundZim staff will never ask for them.</li>
        <li>If you know the person raising money, confirm the campaign with them through a channel you already use.</li>
      </ul>

      <h2>How to report a campaign</h2>
      <p>
        Every campaign page has a <strong>Report this campaign</strong> button. Choose a reason, add details if you can, and
        optionally leave an email address if you are happy to be contacted. Reports are confidential: the organiser is not
        told who reported. The review team looks at every report and decides what to do; a report does not by itself remove
        or pause a campaign.
      </p>
      <p>If someone is in immediate danger, contact the police or local emergency services first.</p>

      <h2>Donations today</h2>
      <p>
        Donations are not available yet. When payments open, they will be handled by licensed payment service providers
        and never through FundZim staff or personal accounts. See <Link href="/how-it-works">How it works</Link>.
      </p>
    </ContentPage>
  );
}
