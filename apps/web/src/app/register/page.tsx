import type { Metadata } from "next";

import { AuthShell, PreviewAccountNotice } from "@/components/auth/auth-shell";
import { RegisterForm } from "@/components/auth/register-form";
import { redirectIfAuthenticated } from "@/lib/auth/session";

export const metadata: Metadata = {
  title: "Create an account",
  robots: { index: false, follow: false },
};

export default async function RegisterPage() {
  await redirectIfAuthenticated("/dashboard");
  return (
    <AuthShell title="Create an account" intro="Join FundZim with your email address." footer={<PreviewAccountNotice />}>
      <RegisterForm />
    </AuthShell>
  );
}
