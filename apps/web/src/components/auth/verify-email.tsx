"use client";

import { useEffect, useRef, useState } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { ButtonLink } from "@/components/ui/button";
import { LoadingSpinner } from "@/components/ui/loading-spinner";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { describeError } from "@/lib/auth/errors";
import { AuthErrorCode } from "@/lib/auth/types";
import { browserNavigation } from "@/lib/browser-navigation";

import { ResendVerificationForm } from "./resend-verification-form";

type State = { kind: "verifying" } | { kind: "verified" } | { kind: "failed"; message: string } | { kind: "missing" };

/**
 * Confirms an email address from the link token (POST /auth/verify-email). Unknown, expired and used tokens
 * get one generic message. The token is removed from the address bar as soon as it has been read.
 */
export function VerifyEmail({ token, api = authApi }: { token: string | null; api?: AuthApi }) {
  const [state, setState] = useState<State>(token ? { kind: "verifying" } : { kind: "missing" });
  const started = useRef(false);
  const outcomeRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    browserNavigation.replaceUrl("/verify-email");
    api
      .verifyEmail(token)
      .then(() => setState({ kind: "verified" }))
      .catch((error: unknown) => {
        const described = describeError(error, "We couldn't confirm your email address. Please try again.");
        setState({
          kind: "failed",
          message:
            described.code === AuthErrorCode.TOKEN_INVALID
              ? "This confirmation link is invalid or has expired. Request a new one below."
              : described.message,
        });
      });
  }, [token, api]);

  useEffect(() => {
    if (state.kind === "verified" || state.kind === "failed") outcomeRef.current?.focus();
  }, [state.kind]);

  if (state.kind === "verifying") return <LoadingSpinner label="Confirming your email address…" />;
  if (state.kind === "verified") {
    return (
      <div className="space-y-4">
        <FormStatus ref={outcomeRef} title="Email address confirmed">
          Thank you. Your email address is confirmed.
        </FormStatus>
        <ButtonLink href="/dashboard">Continue to your dashboard</ButtonLink>
      </div>
    );
  }
  return (
    <div className="space-y-6">
      {state.kind === "failed" ? <FormError ref={outcomeRef} message={state.message} /> : null}
      {state.kind === "missing" ? (
        <p className="text-ink-700">
          Open the link in the email we sent you to confirm your address. If it has expired or did not arrive, request a
          new one.
        </p>
      ) : null}
      <section aria-labelledby="resend-heading" className="space-y-3">
        <h2 id="resend-heading" className="font-display text-xl font-semibold text-ink-900">
          Request a new confirmation link
        </h2>
        <ResendVerificationForm api={api} />
      </section>
    </div>
  );
}
