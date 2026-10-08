/**
 * Runs once when a Next.js server instance starts. In production the Node.js server validates its
 * server-only configuration and refuses to start if it is wrong (see ./instrumentation-node.ts).
 */
export async function register() {
  if (process.env.NEXT_RUNTIME === "nodejs") {
    const { validateServerConfig } = await import("./instrumentation-node");
    validateServerConfig();
  }
}
