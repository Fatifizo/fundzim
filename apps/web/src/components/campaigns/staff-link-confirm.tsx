"use client";

import { useRef, useState } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { Button } from "@/components/ui/button";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";

/**
 * The personal account confirms a link requested by a staff account (ADR-037 §4). Confirmation is an
 * explicit button press from the personal session — never automatic on page load — and the link cannot be
 * removed afterwards through the website.
 */
export function StaffLinkConfirm({ token, api = campaignApi }: { token: string | null; api?: CampaignApi }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  const doneRef = useRef<HTMLDivElement>(null);

  if (!token) return <FormError message="This link is incomplete. Open the link from the email again." />;

  async function confirm() {
    setBusy(true);
    setError(null);
    try {
      await api.confirmStaffLink(token!);
      setDone(true);
      requestAnimationFrame(() => doneRef.current?.focus());
    } catch (err) {
      setError(describeCampaignError(err, "The link could not be confirmed. Please try again.").message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <FormStatus ref={doneRef} title="Your personal account is now linked to your staff account">
        <p>Your staff account can no longer review or decide anything that involves this personal account.</p>
      </FormStatus>
    );
  }
  return (
    <div className="space-y-4">
      <FormError ref={errorRef} message={error} />
      <p className="text-ink-700">
        A FundZim staff account asked to be linked to this personal account. Confirm only if the staff account is yours. Once linked, the staff account is prevented from reviewing your own campaigns and verifications. The link can&apos;t be removed here.
      </p>
      <Button onClick={() => void confirm()} disabled={busy}>
        {busy ? "Confirming…" : "Confirm the link"}
      </Button>
    </div>
  );
}
