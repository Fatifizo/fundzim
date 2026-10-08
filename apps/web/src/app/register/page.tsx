import type { Metadata } from "next";

import { StagePlaceholder } from "@/components/stage-placeholder";

export const metadata: Metadata = {
  title: "Create an account",
  robots: { index: false, follow: false },
};

export default function Page() {
  return (
    <StagePlaceholder
      title="Create an account"
      stage="Stage 4"
      description="Registration is not open yet. FundZim does not collect any personal information in this development preview."
      planned={["Sign up with a phone number (+263 or international) or email address", "Clear consent and privacy choices"]}
    />
  );
}
