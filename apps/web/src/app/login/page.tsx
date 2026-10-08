import type { Metadata } from "next";

import { AuthShell, PreviewAccountNotice } from "@/components/auth/auth-shell";
import { LoginForm } from "@/components/auth/login-form";
import { afterLoginPath, safeNextPath } from "@/lib/auth/safe-redirect";
import { redirectIfAuthenticated } from "@/lib/auth/session";

export const metadata: Metadata = {
  title: "Sign in",
  robots: { index: false, follow: false },
};

export default async function LoginPage({ searchParams }: PageProps<"/login">) {
  const params = await searchParams;
  const next = safeNextPath(typeof params.next === "string" ? params.next : null);
  await redirectIfAuthenticated(afterLoginPath(next));
  return (
    <AuthShell title="Sign in" intro="Welcome back to FundZim." footer={<PreviewAccountNotice />}>
      <LoginForm next={next} />
    </AuthShell>
  );
}
