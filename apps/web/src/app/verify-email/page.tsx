import type { Metadata } from "next";

import { AuthShell } from "@/components/auth/auth-shell";
import { VerifyEmail } from "@/components/auth/verify-email";

export const metadata: Metadata = {
  title: "Confirm your email address",
  robots: { index: false, follow: false },
  referrer: "no-referrer",
};

export default async function VerifyEmailPage({ searchParams }: PageProps<"/verify-email">) {
  const params = await searchParams;
  const token = typeof params.token === "string" && params.token.length > 0 && params.token.length <= 512 ? params.token : null;
  return (
    <AuthShell title="Confirm your email address">
      <VerifyEmail token={token} />
    </AuthShell>
  );
}
