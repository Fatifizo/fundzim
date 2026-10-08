"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { describeError } from "@/lib/auth/errors";

/** Shown while the account's email address is unconfirmed; resends the confirmation link. */
export function EmailVerificationBanner({ email, api = authApi }: { email: string; api?: AuthApi }) {
  const [state, setState] = useState<"idle" | "sending" | "sent" | { error: string }>("idle");

  async function resend() {
    setState("sending");
    try {
      await api.resendVerification(email);
      setState("sent");
    } catch (error) {
      setState({ error: describeError(error, "We couldn't send a new link. Please try again.").message });
    }
  }

  return (
    <section aria-labelledby="verify-banner-title" className="rounded-xl border border-l-4 border-gold-500/50 bg-gold-100 p-4">
      <h2 id="verify-banner-title" className="font-semibold text-gold-800">
        Please confirm your email address
      </h2>
      <p className="mt-1 text-ink-700">
        We sent a confirmation link to <strong>{email}</strong>. Some features stay unavailable until you confirm it.
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button variant="outline" onClick={resend} disabled={state === "sending"}>
          {state === "sending" ? "Sending…" : "Resend confirmation email"}
        </Button>
        <p role="status" className="text-ink-700">
          {state === "sent" ? "A new link is on its way. Check your inbox." : ""}
        </p>
      </div>
      {typeof state === "object" ? (
        <p role="alert" className="mt-2 font-semibold text-danger-700">
          {state.error}
        </p>
      ) : null}
    </section>
  );
}
