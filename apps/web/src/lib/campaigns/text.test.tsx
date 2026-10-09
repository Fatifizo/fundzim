import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { StoryText } from "@/components/campaigns/story-text";

import { characterCount, cleanText, contentProblem, storyParagraphs, truncate } from "./text";

describe("storyParagraphs", () => {
  it("splits on blank lines and keeps single newlines inside a paragraph", () => {
    expect(storyParagraphs("One\nline two\n\nPara two\r\n\r\n\r\nPara three  ")).toEqual(["One\nline two", "Para two", "Para three"]);
    expect(storyParagraphs("\n\n  \n")).toEqual([]);
    expect(storyParagraphs(undefined)).toEqual([]);
  });

  it("strips control and bidi-override characters", () => {
    expect(cleanText("a\u0000b‮c​d\te")).toBe("abcd\te");
  });

  it("counts characters as people see them", () => {
    expect(characterCount("héllo")).toBe(5);
    expect(characterCount("👍🏽")).toBe(2);
    expect(characterCount("a\r\nb")).toBe(3);
  });

  it("truncates on a word boundary", () => {
    expect(truncate("short", 10)).toBe("short");
    expect(truncate("one two three four five", 16)).toBe("one two three…");
    expect(truncate("abcdefghijklmnop", 8)).toBe("abcdefg…");
  });

  it("flags HTML and unsafe links like the API does", () => {
    expect(contentProblem("I <b>need</b> help")).toBe("HTML_NOT_ALLOWED");
    expect(contentProblem("see javascript:alert(1)")).toBe("UNSAFE_LINK");
    expect(contentProblem("I need 2 < 3 and 5 > 4 things")).toBeNull();
    expect(contentProblem("Visit https://example.org")).toBeNull();
  });
});

describe("StoryText rendering safety", () => {
  it("renders hostile text as inert, escaped text: no elements, no links, no handlers", () => {
    const payload = `<script>window.__pwned=1</script>\n\n<img src=x onerror="window.__pwned=2"> https://evil.example javascript:alert(1)\n\n<a href="https://evil.example">click</a>`;
    const { container } = render(<StoryText text={payload} />);
    expect(container.querySelector("script, img, a, iframe")).toBeNull();
    expect(container.querySelectorAll("p")).toHaveLength(3);
    expect(container.textContent).toContain("<script>window.__pwned=1</script>");
    expect(container.textContent).toContain("https://evil.example");
    expect(container.innerHTML).toContain("&lt;script&gt;");
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined();
  });
});
