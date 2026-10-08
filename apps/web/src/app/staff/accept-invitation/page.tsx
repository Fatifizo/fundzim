import type { Metadata } from "next";

import { AuthShell } from "@/components/auth/auth-shell";
import { StaffInvitation } from "@/components/auth/staff-invitation";

export const metadata: Metadata = {
  title: "Accept staff invitation",
  robots: { index: false, follow: false, nocache: true },
  referrer: "no-referrer",
};

export default async function AcceptStaffInvitationPage({ searchParams }: PageProps<"/staff/accept-invitation">) {
  const params = await searchParams;
  const token = typeof params.token === "string" && params.token.length > 0 && params.token.length <= 512 ? params.token : null;
  return (
    <AuthShell title="Accept your staff invitation">
      <StaffInvitation token={token} />
    </AuthShell>
  );
}
