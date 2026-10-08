import type { Metadata } from "next";

import { StagePlaceholder } from "@/components/stage-placeholder";

export const metadata: Metadata = {
  title: "Sign in",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <StagePlaceholder
      title="Sign in"
      stage="Stage 4"
      description="Accounts do not exist yet, so there is nothing to sign in to. Please do not share any passwords or codes on this site."
      planned={["Sign in with your phone number or email address", "One-time codes for sign-in", "Stronger protection for staff accounts"]}
    />
  );
}
