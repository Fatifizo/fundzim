import { describe, expect, it } from "vitest";

import { emailShareUrl, facebookShareUrl, shareLinks, shareText, whatsappShareUrl, xShareUrl } from "./links";

const INPUT = {
  title: "Help Chipo finish school",
  summary: "School fees & uniforms for Chipo's final year — every bit helps!",
  url: "https://fundzim.test/campaigns/help-chipo-abc123",
};

describe("share text", () => {
  it("follows the contract layout with the canonical URL last", () => {
    expect(shareText(INPUT)).toBe(
      "Support this FundZim campaign:\nHelp Chipo finish school\nSchool fees & uniforms for Chipo's final year — every bit helps!\nRead the story: https://fundzim.test/campaigns/help-chipo-abc123",
    );
  });

  it("shortens long summaries on a word boundary and drops control characters", () => {
    const text = shareText({ ...INPUT, summary: `${"word ".repeat(60)}end‮\u0007` });
    const summaryLine = text.split("\n")[2]!;
    expect([...summaryLine].length).toBeLessThanOrEqual(140);
    expect(summaryLine.endsWith("…")).toBe(true);
    expect(text).not.toMatch(/[‮\u0007]/);
  });

  it("omits an empty summary line", () => {
    expect(shareText({ ...INPUT, summary: "   " }).split("\n")).toHaveLength(3);
  });
});

describe("share URLs", () => {
  it("WhatsApp: wa.me with the whole text percent-encoded (newlines %0A, & and spaces encoded)", () => {
    const url = whatsappShareUrl(INPUT);
    expect(url.startsWith("https://wa.me/?text=")).toBe(true);
    const encoded = url.slice("https://wa.me/?text=".length);
    expect(encoded).toContain("%0A");
    expect(encoded).not.toMatch(/[ \n&#?]/);
    expect(encodeURIComponent(decodeURIComponent(encoded))).toBe(encoded);
    expect(new URL(url).searchParams.get("text")).toBe(shareText(INPUT));
  });

  it("Facebook and X carry the encoded canonical URL", () => {
    expect(facebookShareUrl(INPUT)).toBe(`https://www.facebook.com/sharer/sharer.php?u=${encodeURIComponent(INPUT.url)}`);
    const x = new URL(xShareUrl(INPUT));
    expect(x.origin + x.pathname).toBe("https://x.com/intent/post");
    expect(x.searchParams.get("url")).toBe(INPUT.url);
    expect(x.searchParams.get("text")).toBe("Support this FundZim campaign: Help Chipo finish school");
  });

  it("email: mailto with %20 (never +) and the share text as body", () => {
    const mail = emailShareUrl(INPUT);
    expect(mail.startsWith("mailto:?subject=")).toBe(true);
    expect(mail).not.toContain("+");
    const params = new URLSearchParams(mail.slice("mailto:?".length));
    expect(params.get("body")).toBe(shareText(INPUT));
    expect(params.get("subject")).toBe("Support this FundZim campaign: Help Chipo finish school");
  });

  it("a hostile title cannot add parameters or change the target", () => {
    const evil = { ...INPUT, title: "x&u=https://evil.example#?text=hi" };
    expect(new URL(facebookShareUrl(evil)).searchParams.get("u")).toBe(INPUT.url);
    const x = new URL(xShareUrl(evil));
    expect([...x.searchParams.keys()]).toEqual(["text", "url"]);
    expect(x.searchParams.get("url")).toBe(INPUT.url);
    expect(new URL(whatsappShareUrl(evil)).hash).toBe("");
  });

  it("lists the five link channels; only email stays in the tab", () => {
    expect(shareLinks(INPUT).map((l) => [l.channel, l.external])).toEqual([
      ["WHATSAPP", true],
      ["FACEBOOK", true],
      ["X", true],
      ["LINKEDIN", true],
      ["EMAIL", false],
    ]);
  });
});
