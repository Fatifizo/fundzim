import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ApiStatusView } from "@/components/api-status-view";
import { StagePlaceholder } from "@/components/stage-placeholder";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { LoadingSpinner } from "@/components/ui/loading-spinner";
import { axeViolations } from "@/test/axe";

describe("LoadingSpinner", () => {
  it("exposes a status role with an accessible label", () => {
    render(<LoadingSpinner label="Loading campaigns…" />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading campaigns…");
  });
});

describe("ErrorState", () => {
  it("renders as an alert with a reference and action", () => {
    render(<ErrorState reference="digest-123" action={<button type="button">Try again</button>} />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong");
    expect(alert).toHaveTextContent("Reference: digest-123");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });
});

describe("EmptyState", () => {
  it("renders title and description", () => {
    render(<EmptyState title="Nothing here" description="Yet." />);
    expect(screen.getByText("Nothing here")).toBeInTheDocument();
  });
});

describe("ApiStatusView", () => {
  it("shows reachable with version", () => {
    render(
      <ApiStatusView status={{ state: "up", version: { name: "FundZim", version: "0.3.0", build: "b", commit: "0123456789abcdef" } }} />,
    );
    expect(screen.getByText(/Platform API: reachable/)).toHaveTextContent("version 0.3.0 (0123456789ab)");
  });

  it("degrades gracefully when the API is down", () => {
    render(<ApiStatusView status={{ state: "down", code: "NETWORK_ERROR", requestId: "rid-1" }} />);
    expect(screen.getByText(/Platform API: not reachable right now/)).toHaveTextContent("ref rid-1");
  });
});

describe("StagePlaceholder", () => {
  it("is clearly labelled as coming soon, with no forms", async () => {
    const { container } = render(
      <main>
        <StagePlaceholder title="Sign in" stage="Stage 4" description="Accounts do not exist yet." planned={["OTP sign-in"]} />
      </main>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Sign in" })).toBeInTheDocument();
    expect(screen.getByText("Coming soon — under development (Stage 4)")).toBeInTheDocument();
    expect(container.querySelector("form, input, textarea, select")).toBeNull();
    expect(await axeViolations(container)).toEqual([]);
  });
});

describe("axe helper", () => {
  it("detects a known violation (guards against a silently no-op check)", async () => {
    const { container } = render(
      <main>
        {/* eslint-disable-next-line @next/next/no-img-element, jsx-a11y/alt-text */}
        <img src="/x.png" />
      </main>,
    );
    expect((await axeViolations(container)).map((v) => v.id)).toContain("image-alt");
  });
});
