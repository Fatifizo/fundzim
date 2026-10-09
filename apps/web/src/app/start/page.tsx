import { redirect } from "next/navigation";

/**
 * The Stage 3 placeholder URL now starts the campaign wizard (kept so old links keep working). Signed-out
 * visitors are sent to sign in by the proxy and the page itself, then back to the wizard.
 */
export default function StartPage() {
  redirect("/dashboard/campaigns/new");
}
