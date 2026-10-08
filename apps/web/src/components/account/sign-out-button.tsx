"use client";

import { useState } from "react";

import { Button, type ButtonProps } from "@/components/ui/button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { isAuthRequired } from "@/lib/auth/errors";
import { browserNavigation } from "@/lib/browser-navigation";

/** POST /auth/logout, then a full navigation home (clears every signed-in view). */
export function SignOutButton({ api = authApi, ...props }: Omit<ButtonProps, "onClick"> & { api?: AuthApi }) {
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  async function signOut() {
    setBusy(true);
    setFailed(false);
    try {
      await api.logout();
    } catch (error) {
      if (!isAuthRequired(error)) {
        setFailed(true);
        setBusy(false);
        return;
      }
    }
    browserNavigation.assign("/");
  }

  return (
    <>
      <Button {...props} onClick={signOut} disabled={busy}>
        {busy ? "Signing out…" : "Sign out"}
      </Button>
      {failed ? (
        <p role="alert" className="text-sm font-semibold text-danger-700">
          Sign-out failed. Please try again.
        </p>
      ) : null}
    </>
  );
}
