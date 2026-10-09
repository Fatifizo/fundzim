import { redirect } from "next/navigation";

/** The Stage 3 placeholder URL now points to the public campaign listing (kept so old links keep working). */
export default function ExplorePage() {
  redirect("/campaigns");
}
