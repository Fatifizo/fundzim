import Link from "next/link";

import { getSessionOrAnonymous } from "@/lib/auth/session";

import { AccountMenu } from "./account-menu";

/** Request-time header slot: "Sign in" or the account menu, based on the API's view of the session. */
export async function HeaderAccount() {
  const session = await getSessionOrAnonymous();
  if (session.authenticated && session.user) {
    return <AccountMenu displayName={session.user.display_name || session.user.email} />;
  }
  return (
    <Link
      href="/login"
      className="inline-flex min-h-11 items-center rounded-full border-2 border-brand-700 px-4 font-semibold text-brand-700 hover:bg-brand-50"
    >
      Sign In
    </Link>
  );
}

/** Fallback while the session is checked: same footprint, no claim either way. */
export function HeaderAccountFallback() {
  return <span aria-hidden="true" className="inline-block min-h-11 w-24" />;
}
