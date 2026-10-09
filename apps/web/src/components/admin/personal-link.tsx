"use client";

import { useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { looksLikeEmail } from "@/components/auth/use-form-feedback";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import type { StaffLinkStatus } from "@/lib/campaigns/types";

/**
 * Staff → personal account link request (ADR-037 §4; staff session + step-up). The personal account then
 * confirms from its own session using the emailed link. The response never reveals whether the email
 * belongs to an account.
 */
export function PersonalLinkRequest({ initial, api = campaignApi }: { initial: StaffLinkStatus; api?: CampaignApi }) {
  const withStepUp = useStepUp();
  const [status, setStatus] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | undefined>();
  const [sent, setSent] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  const sentRef = useRef<HTMLDivElement>(null);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const email = String(new FormData(event.currentTarget).get("email") ?? "").trim();
    if (!looksLikeEmail(email)) {
      setFieldError("Enter an email address in the format name@example.com.");
      setError("Please correct the errors below.");
      return;
    }
    setFieldError(undefined);
    setError(null);
    setBusy(true);
    try {
      const sentResult = await withStepUp(() => api.requestStaffLink(email));
      setStatus((s) => ({ ...s, status: "PENDING", email_masked: null, expires_at: typeof sentResult?.expires_at === "string" ? sentResult.expires_at : null }));
      setSent(true);
      requestAnimationFrame(() => sentRef.current?.focus());
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setError(describeCampaignError(err, "The request could not be sent.").message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  if (status.status === "LINKED") {
    return (
      <p className="flex flex-wrap items-center gap-2 text-ink-700">
        <Badge tone="brand">Linked</Badge> Your staff account is linked to {status.email_masked ?? "a personal account"}
        {status.linked_at ? ` since ${formatDateTime(status.linked_at)}` : ""}. The link can&apos;t be removed here.
      </p>
    );
  }
  return (
    <div className="space-y-5">
      {status.status === "PENDING" ? (
        <p className="flex flex-wrap items-center gap-2 text-ink-700">
          <Badge tone="info">Waiting for confirmation</Badge> {status.email_masked ? `Requested for ${status.email_masked}.` : ""}
          {status.expires_at ? ` The link expires ${formatDateTime(status.expires_at)}.` : ""}
        </p>
      ) : null}
      {sent ? (
        <FormStatus ref={sentRef} title="Request sent">
          If a personal account with that verified email exists, it receives a link. Sign in to that personal account and open the link to confirm.
        </FormStatus>
      ) : null}
      <form noValidate onSubmit={onSubmit} className="space-y-5">
        <FormError ref={errorRef} message={error} />
        <TextField label="Your personal account's email address" name="email" type="email" inputMode="email" autoComplete="off" autoCapitalize="none" spellCheck={false} hint="The verified login email of your own FundZim personal account." error={fieldError} />
        <p className="text-sm text-ink-700">You will be asked for a code from your authenticator app.</p>
        <SubmitButton busy={busy} busyLabel="Sending…">
          Send link request
        </SubmitButton>
      </form>
    </div>
  );
}
