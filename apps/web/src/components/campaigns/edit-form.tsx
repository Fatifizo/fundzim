"use client";

import { useRouter } from "next/navigation";
import { useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { Alert } from "@/components/ui/alert";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { LIVE_STATUSES } from "@/lib/campaigns/labels";
import { minorToInput } from "@/lib/campaigns/money";
import type { Campaign, CampaignPatch, Category, CurrencyInfo, EligibilityReason } from "@/lib/campaigns/types";
import { goalFromFields, validateSection, type DraftFields } from "@/lib/campaigns/validate";

import { BasicsFields, CategoryField, GoalFields, StoryField } from "./content-fields";
import { EligibilityList } from "./eligibility-list";

const FIELD_MAP: Record<string, string> = { "goal.amount_minor": "amount", "goal.currency": "currency", goal: "amount" };

/**
 * Edit a campaign's content (PATCH with If-Match = version). On a published or paused campaign a material
 * change is reviewed again and the public page keeps the approved version until then (ADR-036 §3).
 */
export function CampaignEditForm({ campaign, categories, currencies, api = campaignApi }: { campaign: Campaign; categories: Category[]; currencies: CurrencyInfo[]; api?: CampaignApi }) {
  const router = useRouter();
  const minor = currencies.find((c) => c.code === campaign.goal?.currency)?.minor_units ?? 2;
  const [fields, setFields] = useState<DraftFields>(() => {
    let amount = "";
    try {
      amount = campaign.goal ? minorToInput(campaign.goal.amount_minor, minor) : "";
    } catch {
      amount = "";
    }
    return { category: campaign.category, title: campaign.title, summary: campaign.summary, story: campaign.story, currency: campaign.goal?.currency ?? "", amount };
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [reasons, setReasons] = useState<EligibilityReason[]>([]);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  const savedRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const live = LIVE_STATUSES.has(String(campaign.status));
  // A goal in a currency that is no longer offered stays selectable so the form can still be saved.
  const options = campaign.goal && !currencies.some((c) => c.code === campaign.goal!.currency) ? [...currencies, { code: campaign.goal.currency, minor_units: minor, display_symbol: campaign.goal.currency }] : currencies;

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaved(false);
    setReasons([]);
    const all = { ...validateSection("category", fields, options), ...validateSection("basics", fields, options), ...validateSection("story", fields, options), ...validateSection("goal", fields, options) };
    setErrors(all);
    if (Object.keys(all).length > 0) {
      setFormError("Please correct the errors below.");
      requestAnimationFrame(() => (formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]') ?? errorRef.current)?.focus());
      return;
    }
    const patch: CampaignPatch = {};
    if (fields.category !== campaign.category) patch.category = fields.category;
    if (fields.title.trim() !== campaign.title) patch.title = fields.title.trim();
    if (fields.summary.trim() !== campaign.summary) patch.summary = fields.summary.trim();
    if (fields.story.trim() !== campaign.story) patch.story = fields.story.trim();
    const goal = goalFromFields(fields, options);
    if (goal && (goal.amount_minor !== campaign.goal?.amount_minor || goal.currency !== campaign.goal?.currency)) patch.goal = goal;
    if (Object.keys(patch).length === 0) {
      setFormError(null);
      setSaved(true);
      requestAnimationFrame(() => savedRef.current?.focus());
      return;
    }
    setBusy(true);
    setFormError(null);
    try {
      await api.update(campaign.id, patch, campaign.version);
      setSaved(true);
      requestAnimationFrame(() => savedRef.current?.focus());
      router.refresh();
    } catch (err) {
      const d = describeCampaignError(err, "Your changes could not be saved. Your text is still here; please try again.");
      const mapped: Record<string, string> = {};
      for (const [k, v] of Object.entries(d.fieldErrors)) mapped[FIELD_MAP[k] ?? k] = v;
      setErrors(mapped);
      setReasons(d.reasons);
      setFormError(d.message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  const change = (p: Partial<DraftFields>) => setFields((f) => ({ ...f, ...p }));
  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-8" aria-label="Campaign details">
      {live ? (
        <Alert tone="notice" title="Changes to a published campaign are reviewed again">
          Changes to the story, goal, category or beneficiary are checked by reviewers. Until they are approved, the public page keeps showing the approved version.
        </Alert>
      ) : null}
      <FormError ref={errorRef} message={formError} />
      {reasons.length > 0 ? <EligibilityList reasons={reasons} allowed={false} allowedText="" context={{ campaignId: campaign.id, organisationId: campaign.organisation_id }} /> : null}
      {saved ? <FormStatus ref={savedRef} title={live ? "Changes saved. They will be reviewed before they appear publicly." : "Changes saved"} /> : null}
      <CategoryField categories={categories} value={fields.category} onChange={change} error={errors.category} hasOrganisation={!!campaign.organisation_id} />
      <BasicsFields value={fields} onChange={change} errors={errors} />
      <StoryField value={fields.story} onChange={change} error={errors.story} />
      <GoalFields value={fields} onChange={change} errors={errors} currencies={options} />
      <SubmitButton busy={busy} busyLabel="Saving…">
        Save changes
      </SubmitButton>
    </form>
  );
}
