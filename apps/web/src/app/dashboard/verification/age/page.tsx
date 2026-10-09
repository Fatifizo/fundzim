import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { AgeAttestationForm } from "@/components/campaigns/age-attestation";
import { requireUser, serverGet } from "@/lib/auth/session";
import { safeNextPath } from "@/lib/auth/safe-redirect";
import { readAgeAttestation } from "@/lib/campaigns/normalise";
import { first } from "@/lib/campaigns/paths";

export const metadata: Metadata = { title: "Confirm your age", robots: { index: false, follow: false, nocache: true } };

const PATH = "/dashboard/verification/age";

export default async function AgeAttestationPage({ searchParams }: PageProps<"/dashboard/verification/age">) {
  const next = safeNextPath(first((await searchParams).next));
  const current = next ? `${PATH}?next=${encodeURIComponent(next)}` : PATH;
  await requireUser(current);
  const attestation = readAgeAttestation(await serverGet<unknown>("/api/v1/me/age-attestation", current));
  return (
    <AccountPage title="Confirm your age" intro="Raising funds on FundZim is for adults. Tell us whether you are an adult.">
      <AgeAttestationForm initial={attestation} next={next} />
    </AccountPage>
  );
}
