import { connection } from "next/server";

/**
 * Liveness probe for containers/load balancers. Deliberately does NOT call the API: the web tier is
 * alive even when the API is down (pages degrade gracefully). Readiness of the API is the API's own
 * /api/v1/ready.
 */
export async function GET() {
  await connection(); // evaluate per request, never prerendered at build time
  return Response.json({ status: "ok" }, { headers: { "Cache-Control": "no-store" } });
}
