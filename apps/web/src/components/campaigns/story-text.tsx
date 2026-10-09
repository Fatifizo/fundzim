import { storyParagraphs } from "@/lib/campaigns/text";
import { cx } from "@/lib/cx";

/**
 * Campaign text as paragraphs of escaped plain text (contract §9): React text nodes only — never
 * dangerouslySetInnerHTML, never auto-linked URLs. Single newlines are kept with `white-space: pre-line`.
 */
export function StoryText({ text, className }: { text: string; className?: string }) {
  const paragraphs = storyParagraphs(text);
  return (
    <div className={cx("space-y-4", className)}>
      {paragraphs.map((p, i) => (
        <p key={i} className="whitespace-pre-line break-words">
          {p}
        </p>
      ))}
    </div>
  );
}
