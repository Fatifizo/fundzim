import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { formatDateTime } from "@/components/account/format";
import { EligibilityList } from "@/components/campaigns/eligibility-list";
import { LifecycleActions } from "@/components/campaigns/lifecycle-actions";
import { CampaignStatusBadge } from "@/components/campaigns/status";
import { StoryText } from "@/components/campaigns/story-text";
import { DefinitionList } from "@/components/verification/status";
import { Alert } from "@/components/ui/alert";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { serverGetOptional, requireUser } from "@/lib/auth/session";
import { humanise, nextEligibilityAction, VISIBILITY_LABELS } from "@/lib/campaigns/labels";
import { formatGoal } from "@/lib/campaigns/money";
import { readEligibility } from "@/lib/campaigns/normalise";
import { first, publicCampaignPath } from "@/lib/campaigns/paths";
import { loadOwnCampaign } from "@/lib/campaigns/server";

export const metadata: Metadata = { title: "Campaign", robots: { index: false, follow: false, nocache: true } };

const ACTION_TEXT: Record<string, { heading: string; ready: string }> = {
  SUBMIT_FOR_REVIEW: { heading: "Before you submit", ready: "Everything needed for review is in place. Submitting does not guarantee approval." },
  PUBLISH: { heading: "Before you publish", ready: "Your approved campaign can be published." },
  REACTIVATE: { heading: "Before you resume", ready: "Your campaign can be resumed." },
};

export default async function CampaignOverviewPage({ params, searchParams }: PageProps<"/dashboard/campaigns/[id]">) {
  const { id } = await params;
  const path = `/dashboard/campaigns/${id}`;
  await requireUser(path);
  const campaign = await loadOwnCampaign(id, path);
  const submitted = first((await searchParams).submitted) === "1";
  const status = String(campaign.status);
  const nextAction = nextEligibilityAction(status);
  let eligibility = nextAction ? campaign.eligibility.find((e) => e.action === nextAction) : undefined;
  if (nextAction && !eligibility) {
    eligibility = readEligibility(await serverGetOptional<unknown>(`/api/v1/campaigns/${campaign.id}/eligibility?action=${nextAction}`, path, null))[0];
  }
  const publicPath = ["ACTIVE", "PAUSED", "COMPLETED"].includes(status) ? publicCampaignPath(campaign.slug) : null;
  const feedback = campaign.review_feedback;

  return (
    <AccountPage title={campaign.title || "Untitled draft"} intro={<CampaignStatusBadge status={status} />}>
      {submitted && status === "SUBMITTED" ? (
        <Alert tone="info" title="Submitted for review">
          Thank you. FundZim reviewers will check your campaign. They may approve it, ask you for changes or decline it — submitting does not guarantee approval. We will let you know the outcome.
        </Alert>
      ) : null}
      {feedback && (status === "CHANGES_REQUESTED" || status === "REJECTED") ? (
        <Alert tone="notice" title={status === "CHANGES_REQUESTED" ? "Reviewers asked for changes" : "Your campaign was not approved"}>
          {feedback.reason_code ? <p className="font-semibold">{humanise(feedback.reason_code)}</p> : null}
          {feedback.message ? <p className="mt-1 whitespace-pre-line break-words">{feedback.message}</p> : null}
          {feedback.decided_at ? <p className="mt-1 text-sm">Decided {formatDateTime(feedback.decided_at)}.</p> : null}
        </Alert>
      ) : null}
      {status === "SUSPENDED" ? (
        <Alert tone="notice" title="This campaign is suspended">
          It is not shown publicly. Contact us if you need help.
        </Alert>
      ) : null}
      {campaign.re_review_required ? (
        <Alert tone="info" title="Changes waiting for review">
          Your recent changes are being reviewed. The public page keeps showing the approved version until then.
        </Alert>
      ) : null}

      <Card as="section" aria-labelledby="overview-title">
        <h2 id="overview-title" className="font-display text-xl font-semibold text-ink-900">
          Overview
        </h2>
        <div className="mt-4">
          <DefinitionList
            items={[
              { term: "Status", value: <CampaignStatusBadge status={status} /> },
              { term: "Goal", value: formatGoal(campaign.goal).text },
              { term: "Category", value: humanise(campaign.category) },
              { term: "Beneficiary", value: campaign.beneficiary ? `${campaign.beneficiary.display_name ?? "Chosen"} (${campaign.beneficiary.disclosure === "NONE" ? "details private" : "name shown"})` : "Not chosen" },
              { term: "Visibility", value: VISIBILITY_LABELS[String(campaign.visibility)]?.label ?? humanise(String(campaign.visibility)) },
              { term: "Created", value: formatDateTime(campaign.created_at) },
              ...(campaign.submitted_at ? [{ term: "Submitted", value: formatDateTime(campaign.submitted_at) }] : []),
              ...(campaign.published_at ? [{ term: "Published", value: formatDateTime(campaign.published_at) }] : []),
              ...(campaign.completion_reason ? [{ term: "Completion reason", value: humanise(campaign.completion_reason) }] : []),
            ]}
          />
        </div>
        <div className="mt-5 flex flex-col gap-3 sm:flex-row sm:flex-wrap">
          {status === "DRAFT" || status === "CHANGES_REQUESTED" ? (
            <ButtonLink href={`/dashboard/campaigns/new?id=${campaign.id}`} variant="outline">
              Continue in the guided steps
            </ButtonLink>
          ) : null}
          {publicPath ? (
            <ButtonLink href={publicPath} variant="ghost">
              View the public page
            </ButtonLink>
          ) : null}
        </div>
      </Card>

      {nextAction && eligibility ? (
        <Card as="section" aria-labelledby="eligibility-title">
          <h2 id="eligibility-title" className="font-display text-xl font-semibold text-ink-900">
            {ACTION_TEXT[nextAction]!.heading}
          </h2>
          <div className="mt-4">
            <EligibilityList reasons={eligibility.reasons} allowed={eligibility.allowed} allowedText={ACTION_TEXT[nextAction]!.ready} context={{ campaignId: campaign.id, organisationId: campaign.organisation_id, returnTo: path }} />
          </div>
        </Card>
      ) : null}

      <Card as="section" aria-labelledby="actions-title">
        <h2 id="actions-title" className="font-display text-xl font-semibold text-ink-900">
          What you can do
        </h2>
        <div className="mt-4">
          <LifecycleActions campaignId={campaign.id} status={status} organisationId={campaign.organisation_id} only={["submit", "withdraw", "publish", "resume", "revise"]} />
        </div>
        <p className="mt-4 text-sm text-ink-600">
          Pause, complete, cancel, archive and visibility are in{" "}
          <Link href={`${path}/settings`} className="font-semibold text-brand-700 underline">
            Settings
          </Link>
          .
        </p>
      </Card>

      <Card as="section" aria-labelledby="content-title">
        <h2 id="content-title" className="font-display text-xl font-semibold text-ink-900">
          Your story (working copy)
        </h2>
        <p className="mt-2 font-semibold break-words text-ink-900">{campaign.summary}</p>
        {campaign.story ? <StoryText text={campaign.story} className="mt-3 text-ink-700" /> : <p className="mt-3 text-ink-700">No story yet.</p>}
      </Card>
    </AccountPage>
  );
}
