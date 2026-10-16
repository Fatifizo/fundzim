import { redirect } from "next/navigation";

import { requireStaff } from "@/lib/auth/session";

/** /admin → the operations command center (every staff role holds ops.dashboard.view). Non-staff get a 404. */
export default async function AdminIndexPage() {
  await requireStaff();
  redirect("/admin/operations");
}
