// Copies static assets next to the standalone server (Next.js does not do this; see the `output` docs).
// Used by the E2E webServer and mirrors what deploy/docker/web.Dockerfile does.
import { cpSync, existsSync } from "node:fs";

cpSync(".next/static", ".next/standalone/.next/static", { recursive: true });
if (existsSync("public")) cpSync("public", ".next/standalone/public", { recursive: true });
