"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { OpsAction } from "@/components/admin/operations/ops-action";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { describeOpsError } from "@/lib/operations/errors";
import { notificationsApi, type NotificationsApi } from "@/lib/notifications/api";
import type { TemplateVersion } from "@/lib/notifications/types";

const REASON = { kind: "textarea", name: "reason", label: "Reason (staff only)" } as const;
const BLOCKED: Record<string, string> = {
  DESTINATION_SUPPRESSED: "the destination is suppressed",
  CONSENT_WITHDRAWN: "the person withdrew consent",
  CONSENT_MISSING: "the person has not given consent",
  PREFERENCE_DISABLED: "the person turned this channel off",
  CONTACT_UNVERIFIED: "the contact is not verified",
  PROVIDER_HAS_MESSAGE: "the provider has the message; its outcome is being resolved instead",
  DELIVERY_NOT_FAILED: "the delivery is no longer failed",
};

/** Replay or discard a dead letter (communications.replay, step-up). The API re-checks consent, suppression and state. */
export function DeadLetterActions({ id, open = true, api = notificationsApi }: { id: string; open?: boolean; api?: NotificationsApi }) {
  const [blocked, setBlocked] = useState<string | null>(null);
  return (
    <div className="space-y-2">
      {blocked ? <p role="status" className="text-sm font-semibold text-ink-900">Not replayed: {BLOCKED[blocked] ?? blocked.toLowerCase().replace(/_/g, " ")}.</p> : null}
      <div className="flex flex-wrap gap-2">
        <OpsAction button="Replay" title="Replay this delivery?" description="Consent, preferences, suppressions, the recipient and the delivery state are checked again before anything is sent."
          confirm="Confirm: Replay" notice="Replay processed." fields={[REASON]} available={open}
          run={async (b) => {
            const r = await api.replay(id, String(b.reason));
            setBlocked(r.replayed ? null : (r.blocked_reason ?? "BLOCKED"));
            return r;
          }} />
        <OpsAction button="Discard" title="Discard this dead letter?" description="Nothing is sent. The record stays." confirm="Confirm: Discard" notice="Dead letter discarded."
          fields={[REASON]} available={open} run={(b) => api.discard(id, String(b.reason))} />
      </div>
    </div>
  );
}

export function LiftSuppression({ id, api = notificationsApi }: { id: string; api?: NotificationsApi }) {
  return (
    <OpsAction button="Lift" title="Lift this suppression?" description="Messages to the destination can be sent again (consent and preferences still apply)."
      confirm="Confirm: Lift" notice="Suppression lifted." fields={[REASON]} run={(b) => api.lift(id, String(b.reason))} />
  );
}

/** Suppress a person's current destination on a channel (the destination is read from the account, never typed). */
export function SuppressForm({ api = notificationsApi }: { api?: NotificationsApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const body = { user_id: String(f.get("user_id") ?? "").trim(), channel: String(f.get("channel") ?? ""), reason: String(f.get("reason") ?? "") };
    if (!/^[0-9a-f-]{36}$/i.test(body.user_id)) {
      setError("Paste the person's account id.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const key = crypto.randomUUID();
      await withStepUp(() => api.suppress(body, key));
      setNotice("Destination suppressed.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) setError(describeOpsError(err).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={submit} aria-label="Suppress a destination" className="space-y-4 rounded-card border border-line bg-surface p-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError message={error} />
      <TextField label="Account id" name="user_id" autoComplete="off" />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField label="Channel" name="channel" options={["EMAIL", "SMS", "WHATSAPP"].map((v) => ({ value: v, label: v === "EMAIL" ? "E-mail" : v === "SMS" ? "SMS" : "WhatsApp" }))} />
        <SelectField label="Reason" name="reason" options={[{ value: "USER_OPT_OUT", label: "The person opted out" }, { value: "SECURITY_RESTRICTION", label: "Security restriction" },
          { value: "LEGAL_RESTRICTION", label: "Legal restriction" }, { value: "INVALID_DESTINATION", label: "Invalid destination" }]} />
      </div>
      <SubmitButton busy={busy}>Suppress</SubmitButton>
    </form>
  );
}

const ACTION_COPY: Record<string, { button: string; title: string; description: string; reason: boolean }> = {
  submit: { button: "Submit for approval", title: "Submit this version for approval?", description: "The content is frozen. A different person must approve it.", reason: false },
  approve: { button: "Approve", title: "Approve this version?", description: "Checker step: never the maker, the submitter or an account linked to them.", reason: true },
  reject: { button: "Reject", title: "Reject this version?", description: "The version is retired; draft a new one instead.", reason: true },
  activate: { button: "Activate", title: "Activate this version?", description: "New notifications use it from now on; the previous version is retired. Sent notifications keep theirs.", reason: true },
  retire: { button: "Retire", title: "Retire this version?", description: "It will never be used.", reason: true },
  "whatsapp-simulator": { button: "Submit WhatsApp template to the simulator", title: "Submit to the WhatsApp simulator?", description: "TEST only: the result is SIMULATOR_ACCEPTED or SIMULATOR_REJECTED, never a provider approval.", reason: false },
};

export function TemplateVersionActions({ version, actions, api = notificationsApi }: { version: TemplateVersion; actions: string[]; api?: NotificationsApi }) {
  return (
    <div className="flex flex-wrap gap-2">
      {actions.map((a) => {
        const c = ACTION_COPY[a]!;
        return (
          <OpsAction key={a} button={c.button} title={c.title} description={c.description} confirm={`Confirm: ${c.button}`} notice="Done."
            fields={c.reason ? [REASON] : []} body={{ version: version.version }} run={(b) => api.templateAction(version.id, a, b)} />
        );
      })}
    </div>
  );
}

/** Draft a new version (maker). Pre-filled from the active version; {{variable}} placeholders from the allow-list only. */
export function DraftForm({ templateId, base, allowed, api = notificationsApi }: { templateId: string; base: TemplateVersion | null; allowed: string[]; api?: NotificationsApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    const v = (k: string) => {
      const x = String(f.get(k) ?? "").trim();
      return x === "" ? null : x;
    };
    const body = { inapp_title: v("inapp_title") ?? "", inapp_body: v("inapp_body") ?? "", email_subject: v("email_subject"), email_text: v("email_text"), sms_text: v("sms_text") };
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.createDraft(templateId, body, crypto.randomUUID()));
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) setError(describeOpsError(err).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={submit} aria-label="Draft a new version" className="space-y-4 rounded-card border border-line bg-surface p-4">
      <FormError message={error} />
      <p className="text-sm text-ink-600">Allowed variables: {["display_name", ...allowed].map((a) => `{{${a}}}`).join(", ")}. No other placeholders, no HTML.</p>
      <TextField label="In-app title" name="inapp_title" defaultValue={base?.inapp_title} maxLength={200} />
      <TextAreaField label="In-app text" name="inapp_body" defaultValue={base?.inapp_body} maxLength={2000} />
      <TextField label="E-mail subject (optional)" name="email_subject" defaultValue={base?.email_subject ?? ""} maxLength={200} />
      <TextAreaField label="E-mail text (optional)" name="email_text" defaultValue={base?.email_text ?? ""} maxLength={8000} />
      <TextAreaField label="SMS text (optional)" name="sms_text" defaultValue={base?.sms_text ?? ""} maxLength={480} />
      <SubmitButton busy={busy}>Save draft</SubmitButton>
    </form>
  );
}

/** Renders a version with sample values; nothing is sent. */
export function PreviewButton({ versionId, api = notificationsApi }: { versionId: string; api?: NotificationsApi }) {
  const [out, setOut] = useState<Record<string, Record<string, unknown>> | null>(null);
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="space-y-2">
      <Button variant="outline" onClick={() => api.preview(versionId).then(setOut).catch(() => setError("The preview could not be rendered."))}>
        Preview
      </Button>
      <FormError message={error} />
      {out ? (
        <dl className="space-y-2 rounded-card border border-line bg-surface p-4 text-sm">
          {Object.entries(out).map(([ch, v]) => (
            <div key={ch}>
              <dt className="font-semibold">{ch.replace("_", "-")}</dt>
              <dd className="whitespace-pre-wrap break-words">
                {Object.entries(v)
                  .filter(([k]) => k !== "html")
                  .map(([k, val]) => `${k}: ${Array.isArray(val) ? val.join(", ") : String(val)}`)
                  .join("\n")}
              </dd>
            </div>
          ))}
        </dl>
      ) : null}
    </div>
  );
}
