import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

afterEach(() => {
  cleanup();
});

// The App Router context does not exist in unit tests; provide a deterministic pathname.
vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn(), back: vi.fn(), forward: vi.fn(), prefetch: vi.fn() }),
  notFound: () => {
    throw new Error("NEXT_NOT_FOUND");
  },
  redirect: (url: string) => {
    throw new Error(`NEXT_REDIRECT:${url}`);
  },
}));

// The header's session slot is an async Server Component that calls the API (server-only); jsdom cannot
// render it. Unit tests see the anonymous state; the real component is covered by the E2E suite.
vi.mock("@/components/layout/header-account", () => ({
  HeaderAccount: () => <a href="/login">Sign In</a>,
  HeaderAccountFallback: () => null,
}));
