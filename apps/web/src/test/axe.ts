import axe from "axe-core";

/**
 * Runs axe-core against a rendered container and returns violations at or above "serious" plus all
 * "moderate" ones. jsdom has no layout or canvas, so `color-contrast` cannot be evaluated here; contrast is
 * covered by the Playwright axe scans in a real browser (e2e/) and by the token notes in globals.css.
 */
export async function axeViolations(container: Element) {
  const results = await axe.run(container, {
    rules: { "color-contrast": { enabled: false } },
    resultTypes: ["violations"],
  });
  return results.violations.map((v) => ({ id: v.id, impact: v.impact, nodes: v.nodes.map((n) => n.target.join(" ")) }));
}
