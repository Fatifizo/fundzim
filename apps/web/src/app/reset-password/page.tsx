import type { Metadata } from "next";

import { AuthShell } from "@/components/auth/auth-shell";
import { ResetPasswordForm } from "@/components/auth/reset-password-form";

export const metadata: Metadata = {
  title: "Choose a new password",
  robots: { index: false, follow: false },
  referrer: "no-referrer",
};

export default async function ResetPasswordPage({ searchParams }: PageProps<"/reset-password">) {
  const params = await searchParams;
  const token = typeof params.token === "string" && params.token.length > 0 && params.token.length <= 512 ? params.token : null;
  return (
    <AuthShell title="Choose a new password">
      <ResetPasswordForm queryToken={token} />
    </AuthShell>
  );
}
