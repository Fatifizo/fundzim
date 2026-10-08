import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { SiteHeader } from "@/components/layout/site-header";

describe("SiteHeader", () => {
  it("renders the wordmark link and the primary navigation", () => {
    render(<SiteHeader />);
    expect(screen.getByRole("link", { name: "FundZim home" })).toHaveAttribute("href", "/");
    const nav = screen.getByRole("navigation", { name: "Main" });
    const links = within(nav).getAllByRole("link");
    expect(links.map((l) => [l.textContent, l.getAttribute("href")])).toEqual([
      ["Explore", "/explore"],
      ["How It Works", "/how-it-works"],
      ["Start a Fundraiser", "/start"],
    ]);
    // Sign-in / account menu sits outside the main nav (session-aware slot, mocked in src/test/setup.tsx).
    expect(screen.getByRole("link", { name: "Sign In" })).toHaveAttribute("href", "/login");
  });

  it("toggles the mobile menu with aria-expanded", async () => {
    const user = userEvent.setup();
    render(<SiteHeader />);
    const button = screen.getByRole("button", { name: "Menu" });
    expect(button).toHaveAttribute("aria-expanded", "false");
    const panelId = button.getAttribute("aria-controls");
    expect(panelId).toBeTruthy();
    const panel = document.getElementById(panelId!);
    expect(panel).not.toBeVisible();

    await user.click(button);
    expect(button).toHaveAttribute("aria-expanded", "true");
    expect(panel).toBeVisible();
    expect(within(panel!).getByRole("link", { name: "How It Works" })).toBeVisible();

    await user.click(button);
    expect(button).toHaveAttribute("aria-expanded", "false");
  });

  it("closes on Escape and returns focus to the menu button", async () => {
    const user = userEvent.setup();
    render(<SiteHeader />);
    const button = screen.getByRole("button", { name: "Menu" });
    await user.click(button);
    const panel = document.getElementById(button.getAttribute("aria-controls")!);
    await user.tab();
    expect(panel).toContainElement(document.activeElement as HTMLElement);

    await user.keyboard("{Escape}");
    expect(button).toHaveAttribute("aria-expanded", "false");
    expect(panel).not.toBeVisible();
    expect(button).toHaveFocus();
  });

  it("closes when a menu link is chosen", async () => {
    const user = userEvent.setup();
    render(<SiteHeader />);
    const button = screen.getByRole("button", { name: "Menu" });
    await user.click(button);
    const panel = document.getElementById(button.getAttribute("aria-controls")!)!;
    await user.click(within(panel).getByRole("link", { name: "How It Works" }));
    expect(button).toHaveAttribute("aria-expanded", "false");
  });
});
