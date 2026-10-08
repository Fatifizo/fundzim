import type { Metadata } from "next";
import Link from "next/link";
import { redirect } from "next/navigation";

import { AuthShell } from "@/components/auth/auth-shell";
import { MfaChallengeForm } from "@/components/auth/mfa-challenge-form";
import { afterLoginPath, safeNextPath } from "@/lib/auth/safe-redirect";
import { getSession, SessionUnavailableError } from "@/lib/auth/session";

export const metadata: Metadata = {
  title: "Two-step verification",
  robots: { index: false, follow: false },
};

export default async function MfaPage({ searchParams }: PageProps<"/login/mfa">) {
  const params = await searchParams;
  const next = safeNextPath(typeof params.next === "string" ? params.next : null);
  let pending = true;
  let authenticated = false;
  try {
    const session = await getSession();
    authenticated = session.authenticated;
    pending = session.mfa_pending === true;
  } catch (error) {
    // API unreachable: show the form; submitting will report the outage.
    if (!(error instanceof SessionUnavailableError)) throw error;
  }
  if (authenticated) redirect(afterLoginPath(next));

  return (
    <AuthShell title="Two-step verification" intro="Your account is protected with an authenticator app.">
      {pending ? (
        <MfaChallengeForm next={next} />
      ) : (
        <div className="space-y-4">
          <p className="text-ink-700">There is no sign-in waiting for a code, or it has expired. Please sign in again.</p>
          <Link href="/login" className="inline-flex min-h-11 items-center rounded-full bg-brand-700 px-5 font-semibold text-white hover:bg-brand-800">
            Sign in
          </Link>
        </div>
      )}
    </AuthShell>
  );
}
