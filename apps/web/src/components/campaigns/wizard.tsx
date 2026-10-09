"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { LoadingSpinner } from "@/components/ui/loading-spinner";
import { isAuthRequired } from "@/lib/auth/errors";
import { loginUrl } from "@/lib/auth/safe-redirect";
import { browserNavigation } from "@/lib/browser-navigation";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { WIZARD_STEPS, type WizardStep } from "@/lib/campaigns/eligibility";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { formatGoal, minorToInput } from "@/lib/campaigns/money";
import { mediaReadiness } from "@/lib/campaigns/progress";
import type { Campaign, CampaignMedia, CampaignPatch, Category, CurrencyInfo, EligibilityReason, EligibilityResult } from "@/lib/campaigns/types";
import { goalFromFields, validateSection, type DraftFields, type DraftSection } from "@/lib/campaigns/validate";
import type { Beneficiary, MyOrganisation } from "@/lib/verification/types";

import { BeneficiaryPicker } from "./beneficiary-picker";
import { BasicsFields, CategoryField, GoalFields, StoryField } from "./content-fields";
import { EligibilityList } from "./eligibility-list";
import { MediaManager } from "./media-manager";

const SECTION_FOR_STEP: Partial<Record<WizardStep, DraftSection>> = { 1: "category", 2: "basics", 3: "story", 4: "goal" };
const EDITABLE = new Set(["DRAFT", "CHANGES_REQUESTED"]);

/** Maps API field names onto the wizard's fields. */
const FIELD_MAP: Record<string, string> = { "goal.amount_minor": "amount", "goal.currency": "currency", goal: "amount", category_code: "category" };
const FIELD_STEP: Record<string, WizardStep> = { category: 1, title: 2, summary: 2, story: 3, amount: 4, currency: 4 };

function fieldsFrom(c: Campaign | null, currencies: CurrencyInfo[]): DraftFields {
  if (!c) return { category: "", title: "", summary: "", story: "", currency: currencies.length === 1 ? currencies[0]!.code : "", amount: "" };
  const minor = currencies.find((x) => x.code === c.goal?.currency)?.minor_units ?? 2;
  let amount = "";
  try {
    amount = c.goal ? minorToInput(c.goal.amount_minor, minor) : "";
  } catch {
    amount = "";
  }
  return { category: c.category, title: c.title, summary: c.summary, story: c.story, currency: c.goal?.currency ?? "", amount };
}

export interface WizardProps {
  categories: Category[];
  currencies: CurrencyInfo[];
  beneficiaries: Beneficiary[];
  organisations: MyOrganisation[];
  initialCampaign: Campaign | null;
  initialMedia: CampaignMedia[];
  initialStep: WizardStep;
  verificationLevel: string | null;
  api?: CampaignApi;
}

/**
 * Guided campaign creation (8 steps). Steps 1–4 are held in the page until a goal is set: the API needs a
 * category, title, summary and goal to create a draft (contract §6), so step 4 creates it (with an
 * Idempotency-Key, so a retried click cannot create two drafts) and later steps edit it with If-Match. The
 * URL then carries the draft id and step, so a reload or a detour to fix something resumes here. Input is
 * kept on every error. Submission sends the campaign to review; it never implies approval.
 */
export function CampaignWizard({ categories, currencies, beneficiaries, organisations, initialCampaign, initialMedia, initialStep, verificationLevel, api = campaignApi }: WizardProps) {
  const router = useRouter();
  const [campaign, setCampaign] = useState<Campaign | null>(initialCampaign);
  const [fields, setFields] = useState<DraftFields>(() => fieldsFrom(initialCampaign, currencies));
  const [organisationId, setOrganisationId] = useState<string>(initialCampaign?.organisation_id ?? "");
  const [step, setStep] = useState<WizardStep>(initialStep);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [reasons, setReasons] = useState<EligibilityReason[]>([]);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [media, setMedia] = useState<CampaignMedia[]>(initialMedia);
  const [eligibility, setEligibility] = useState<EligibilityResult | null>(null);
  const idempotencyKey = useRef<string>("");
  const headingRef = useRef<HTMLHeadingElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const firstRender = useRef(true);

  const adminOrgs = organisations.filter((o) => o.my_role === "ORG_ADMIN" || o.my_permissions?.includes("org.campaign.create"));
  const editable = !campaign || EDITABLE.has(String(campaign.status));
  const stepHref = useCallback((s: WizardStep) => (campaign ? `/dashboard/campaigns/new?id=${campaign.id}&step=${s}` : `/dashboard/campaigns/new?step=${s}`), [campaign]);
  const fixContext = { campaignId: campaign?.id, organisationId: campaign?.organisation_id ?? null, stepHref: (s: WizardStep) => stepHref(s), returnTo: stepHref(7) };

  // Focus the step heading after navigation between steps (not on first load).
  useEffect(() => {
    if (firstRender.current) {
      firstRender.current = false;
      return;
    }
    headingRef.current?.focus();
  }, [step]);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  const goTo = useCallback(
    (next: WizardStep, c: Campaign | null = campaign) => {
      setErrors({});
      setFormError(null);
      setReasons([]);
      setStep(next);
      if (c) window.history.replaceState(null, "", `/dashboard/campaigns/new?id=${c.id}&step=${next}`);
      window.scrollTo({ top: 0 });
    },
    [campaign],
  );

  // Eligibility is (re)checked whenever step 7 or 8 is shown, and on "Check again". State is only set in the
  // request's callbacks; `checking` is derived from which request the current result belongs to.
  const [checkTick, setCheckTick] = useState(0);
  const [checkedKey, setCheckedKey] = useState("");
  const checkKey = campaign && (step === 7 || step === 8) ? `${campaign.id}:${step}:${checkTick}` : "";
  const checking = checkKey !== "" && checkedKey !== checkKey;
  const campaignId = campaign?.id;
  useEffect(() => {
    if (!checkKey || !campaignId) return;
    let active = true;
    api.eligibility(campaignId, "SUBMIT_FOR_REVIEW").then(
      (result) => {
        if (!active) return;
        setEligibility(result);
        setCheckedKey(checkKey);
      },
      (error) => {
        if (!active) return;
        setEligibility(null);
        setCheckedKey(checkKey);
        showError(error, "We couldn't check your campaign. Please try again.");
      },
    );
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- showError is a stable-enough render helper
  }, [api, campaignId, checkKey]);

  function change(patch: Partial<DraftFields>) {
    setFields((f) => ({ ...f, ...patch }));
    setNotice(null);
  }

  function showError(error: unknown, fallback: string): boolean {
    if (isAuthRequired(error)) {
      browserNavigation.assign(loginUrl(browserNavigation.currentPath()));
      return true;
    }
    const d = describeCampaignError(error, fallback);
    const mapped: Record<string, string> = {};
    for (const [k, v] of Object.entries(d.fieldErrors)) mapped[FIELD_MAP[k] ?? k] = v;
    setErrors(mapped);
    setReasons(d.reasons);
    setFormError(d.message);
    requestAnimationFrame(() => errorRef.current?.focus());
    // A field error on another step: go there so it can be fixed.
    const target = Object.keys(mapped).map((k) => FIELD_STEP[k]).find(Boolean);
    if (target && target !== step) setStep(target);
    return false;
  }

  function validate(sections: DraftSection[]): boolean {
    const all: Record<string, string> = {};
    for (const s of sections) Object.assign(all, validateSection(s, fields, currencies));
    setErrors(all);
    if (Object.keys(all).length === 0) return true;
    setFormError("Please correct the errors below.");
    requestAnimationFrame(() => (document.querySelector<HTMLElement>('main [aria-invalid="true"]') ?? errorRef.current)?.focus());
    return false;
  }

  /** PATCH only the fields that differ from the saved draft. */
  function diff(c: Campaign): CampaignPatch {
    const patch: CampaignPatch = {};
    if (fields.category && fields.category !== c.category) patch.category = fields.category;
    if (fields.title.trim() && fields.title.trim() !== c.title) patch.title = fields.title.trim();
    if (fields.summary.trim() && fields.summary.trim() !== c.summary) patch.summary = fields.summary.trim();
    if (fields.story.trim() && fields.story.trim() !== c.story) patch.story = fields.story.trim();
    const goal = goalFromFields(fields, currencies);
    if (goal && (goal.amount_minor !== c.goal?.amount_minor || goal.currency !== c.goal?.currency)) patch.goal = goal;
    return patch;
  }

  /** Creates the draft (first time) or saves changes. Returns the saved campaign, or null on error. */
  async function save(): Promise<Campaign | null> {
    if (campaign) {
      const patch = diff(campaign);
      if (Object.keys(patch).length === 0) return campaign;
      const updated = await api.update(campaign.id, patch, campaign.version);
      setCampaign(updated);
      return updated;
    }
    const goal = goalFromFields(fields, currencies);
    if (!goal) return null;
    if (!idempotencyKey.current) idempotencyKey.current = crypto.randomUUID();
    let created = await api.create(
      { title: fields.title.trim(), summary: fields.summary.trim(), category: fields.category, goal, ...(organisationId ? { organisation_id: organisationId } : {}) },
      idempotencyKey.current,
    );
    setCampaign(created);
    if (fields.story.trim()) {
      created = await api.update(created.id, { story: fields.story.trim() }, created.version);
      setCampaign(created);
    }
    return created;
  }

  async function onNext() {
    setNotice(null);
    const section = SECTION_FOR_STEP[step];
    if (section && editable && !validate([section])) return;
    if (step < 4 || !editable) return goTo((step + 1) as WizardStep);
    if (step === 4) {
      if (!validate(["category", "basics", "story", "goal"])) {
        const firstBad = Object.keys(validateSectionAll()).map((k) => FIELD_STEP[k]).find(Boolean);
        if (firstBad && firstBad !== 4) setStep(firstBad);
        return;
      }
      setBusy(true);
      try {
        const saved = await save();
        if (saved) goTo(5, saved);
      } catch (error) {
        showError(error, "Your draft could not be saved. Your text is still here; please try again.");
      } finally {
        setBusy(false);
      }
      return;
    }
    goTo((step + 1) as WizardStep);
  }

  function validateSectionAll() {
    return { ...validateSection("category", fields, currencies), ...validateSection("basics", fields, currencies), ...validateSection("story", fields, currencies), ...validateSection("goal", fields, currencies) };
  }

  async function onSaveDraft() {
    setNotice(null);
    setFormError(null);
    setBusy(true);
    try {
      const saved = await save();
      if (saved) {
        setNotice("Draft saved. You can come back to it from Your campaigns.");
        window.history.replaceState(null, "", `/dashboard/campaigns/new?id=${saved.id}&step=${step}`);
      }
    } catch (error) {
      showError(error, "Your draft could not be saved. Your text is still here; please try again.");
    } finally {
      setBusy(false);
    }
  }

  async function onSubmit() {
    if (!campaign) return;
    setBusy(true);
    setFormError(null);
    try {
      await save();
      await api.lifecycle(campaign.id, "submit");
      router.push(`/dashboard/campaigns/${campaign.id}?submitted=1`);
    } catch (error) {
      showError(error, "Your campaign could not be submitted. Please try again.");
      setBusy(false);
    }
  }

  const canSaveDraft = editable && (campaign !== null || goalFromFields(fields, currencies) !== null) && !!fields.category && fields.title.trim() !== "" && fields.summary.trim() !== "";
  const ready = mediaReadiness(media);

  return (
    <div className="space-y-6">
      {verificationLevel === "UNVERIFIED" ? (
        <Alert tone="notice" title="Before you can save a campaign">
          <p>
            Creating a campaign needs basic verification: a confirmed email address and phone number, and a declaration that you are an adult.{" "}
            <Link href={`/dashboard/verification/age?next=${encodeURIComponent("/dashboard/campaigns/new")}`} className="font-semibold text-brand-700 underline">
              Confirm your age
            </Link>{" "}
            or{" "}
            <Link href="/dashboard/verification" className="font-semibold text-brand-700 underline">
              check your verification
            </Link>
            .
          </p>
        </Alert>
      ) : null}

      <nav aria-label="Campaign steps">
        <ol className="flex flex-wrap gap-2 text-sm">
          {WIZARD_STEPS.map((label, i) => {
            const n = (i + 1) as WizardStep;
            const current = n === step;
            return (
              <li key={label} aria-current={current ? "step" : undefined} className={current ? "rounded-full bg-brand-700 px-3 py-1 font-semibold text-white" : n < step ? "rounded-full bg-brand-50 px-3 py-1 text-brand-800" : "rounded-full bg-surface-muted px-3 py-1 text-ink-700"}>
                <span className="sr-only">{current ? "Current step: " : n < step ? "Done: " : ""}</span>
                {n}. {label}
              </li>
            );
          })}
        </ol>
      </nav>

      <Card as="section" aria-labelledby="wizard-step-title" className="space-y-6">
        <h2 id="wizard-step-title" ref={headingRef} tabIndex={-1} className="font-display text-2xl font-semibold text-ink-900 focus:outline-none">
          Step {step} of 8: {WIZARD_STEPS[step - 1]}
        </h2>
        {!editable ? (
          <Alert tone="info" title="This campaign can no longer be edited here">
            It has been submitted or decided. <Link href={`/dashboard/campaigns/${campaign!.id}`} className="font-semibold text-brand-700 underline">Go to the campaign</Link>.
          </Alert>
        ) : null}
        <FormError ref={errorRef} message={formError} />
        {reasons.length > 0 ? <EligibilityList reasons={reasons} allowed={false} allowedText="" context={fixContext} /> : null}
        {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}

        {step === 1 ? (
          <div className="space-y-6">
            {!campaign && adminOrgs.length > 0 ? (
              <fieldset className="space-y-2">
                <legend className="font-semibold text-ink-900">Raise funds as</legend>
                <label className="flex min-h-11 items-center gap-3">
                  <input type="radio" name="owner" checked={organisationId === ""} onChange={() => setOrganisationId("")} className="size-5 accent-brand-700" />
                  Yourself
                </label>
                {adminOrgs.map((o) => (
                  <label key={o.id} className="flex min-h-11 items-center gap-3">
                    <input type="radio" name="owner" checked={organisationId === o.id} onChange={() => setOrganisationId(o.id)} className="size-5 accent-brand-700" />
                    <span className="break-words">{o.display_name} (organisation)</span>
                  </label>
                ))}
              </fieldset>
            ) : null}
            <CategoryField categories={categories} value={fields.category} onChange={change} error={errors.category} hasOrganisation={!!organisationId} />
          </div>
        ) : null}
        {step === 2 ? <BasicsFields value={fields} onChange={change} errors={errors} /> : null}
        {step === 3 ? <StoryField value={fields.story} onChange={change} error={errors.story} /> : null}
        {step === 4 ? (
          currencies.length === 0 ? (
            <Alert tone="notice" title="No currency is available">
              FundZim can&apos;t accept a goal in any currency right now. Please try again later.
            </Alert>
          ) : (
            <GoalFields value={fields} onChange={change} errors={errors} currencies={currencies} />
          )
        ) : null}
        {step === 5 && campaign ? (
          <BeneficiaryPicker
            campaignId={campaign.id}
            organisationId={campaign.organisation_id}
            beneficiaries={beneficiaries}
            current={campaign.beneficiary}
            editable={editable}
            onLinked={(b) => setCampaign((c) => (c ? { ...c, beneficiary: b } : c))}
            api={api}
          />
        ) : null}
        {step === 6 && campaign ? (
          <div className="space-y-4">
            <p className="text-ink-700">A cover photo is required. Photos are checked for safety and processed before anyone can see them; this usually takes a minute.</p>
            <MediaManager campaignId={campaign.id} initial={media} editable={editable} onChange={setMedia} api={api} />
            {ready.coverApproved ? null : <p className="text-sm text-ink-700">You can continue while photos are being checked; the next step shows what is still needed.</p>}
          </div>
        ) : null}
        {step === 7 && campaign ? (
          <div className="space-y-4">
            <p className="text-ink-700">We check the things FundZim needs before your campaign can be reviewed. Fix anything listed, then check again.</p>
            {checking ? <LoadingSpinner label="Checking your campaign…" /> : null}
            {eligibility && !checking ? (
              <EligibilityList id="eligibility" reasons={eligibility.reasons} allowed={eligibility.allowed} allowedText="Everything needed for review is in place." context={fixContext} />
            ) : null}
            <Button variant="outline" onClick={() => setCheckTick((t) => t + 1)} disabled={checking}>
              Check again
            </Button>
          </div>
        ) : null}
        {step === 8 && campaign ? (
          <div className="space-y-4">
            <dl className="grid gap-3 text-ink-700 sm:grid-cols-2">
              <div>
                <dt className="text-sm font-semibold text-ink-600">Title</dt>
                <dd className="break-words">{campaign.title}</dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Goal</dt>
                <dd>{formatGoal(campaign.goal, currencies.find((c) => c.code === campaign.goal?.currency)?.minor_units).text}</dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Beneficiary</dt>
                <dd className="break-words">{campaign.beneficiary?.display_name ?? (campaign.beneficiary ? "Chosen" : "Not chosen")}</dd>
              </div>
              <div>
                <dt className="text-sm font-semibold text-ink-600">Cover photo</dt>
                <dd>{ready.coverApproved ? "Ready" : "Not ready"}</dd>
              </div>
            </dl>
            <Alert tone="info" title="What happens when you submit">
              <p>
                FundZim reviewers check your campaign against our policies. They may approve it, ask you for changes, or decline it: submitting does not guarantee approval. You can withdraw it while it waits for review. If it is approved, you decide when to publish it.
              </p>
            </Alert>
            {eligibility && !eligibility.allowed ? (
              <EligibilityList reasons={eligibility.reasons} allowed={false} allowedText="" context={fixContext} />
            ) : null}
          </div>
        ) : null}

        <div className="flex flex-col-reverse gap-3 border-t border-line pt-5 sm:flex-row sm:flex-wrap sm:items-center">
          {step > 1 ? (
            <Button variant="ghost" onClick={() => goTo((step - 1) as WizardStep)} disabled={busy}>
              Back
            </Button>
          ) : null}
          {step <= 4 && editable ? (
            <Button variant="outline" onClick={() => void onSaveDraft()} disabled={busy || !canSaveDraft} aria-describedby={canSaveDraft ? undefined : "save-draft-hint"}>
              Save draft
            </Button>
          ) : null}
          {step < 8 ? (
            <Button onClick={() => void onNext()} disabled={busy || (step >= 5 && !campaign)} className="sm:ml-auto">
              {busy ? "Saving…" : step === 4 && !campaign ? "Save and continue" : step === 7 && eligibility?.allowed ? "Continue to submit" : "Next"}
            </Button>
          ) : (
            <Button onClick={() => void onSubmit()} disabled={busy || !editable || !eligibility?.allowed} className="sm:ml-auto">
              {busy ? "Submitting…" : "Submit for review"}
            </Button>
          )}
        </div>
        {step <= 4 && !canSaveDraft && editable ? (
          <p id="save-draft-hint" className="text-sm text-ink-600">
            A draft can be saved once it has a category, title, summary and goal.
          </p>
        ) : null}
      </Card>
    </div>
  );
}
