import { Fragment } from "react";

import type { Segment } from "@/lib/discovery/types";

/** Text with the words that matched a search marked (<mark>); plain text only (React escapes every segment). */
export function Highlighted({ text, segments }: { text: string; segments: Segment[] | null | undefined }) {
  if (!segments || segments.length === 0) return <>{text}</>;
  return (
    <>
      {segments.map((s, i) =>
        s.match ? (
          <mark key={i} className="rounded-sm bg-gold-200 px-0.5 text-ink-900">
            {s.text}
          </mark>
        ) : (
          // bare text nodes: wrapping plain text in elements would let accessible-name computation drop the spaces at
          // element boundaries ("Help" + "Tariro" read as "HelpTariro")
          <Fragment key={i}>{s.text}</Fragment>
        ),
      )}
    </>
  );
}
