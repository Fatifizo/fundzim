import type { ReactNode } from "react";

import { cx } from "@/lib/cx";

export interface SectionHeadingProps {
  id?: string;
  eyebrow?: string;
  title: string;
  description?: ReactNode;
  level?: 1 | 2 | 3;
  align?: "left" | "center";
  className?: string;
}

export function SectionHeading({ id, eyebrow, title, description, level = 2, align = "left", className }: SectionHeadingProps) {
  const Heading = `h${level}` as const;
  return (
    <div className={cx("max-w-2xl", align === "center" && "mx-auto text-center", className)}>
      {eyebrow ? <p className="text-sm font-semibold tracking-wide text-brand-700 uppercase">{eyebrow}</p> : null}
      <Heading
        id={id}
        className={cx(
          "font-display font-bold tracking-tight text-ink-900",
          level === 1 ? "mt-2 text-3xl sm:text-4xl" : "mt-1 text-2xl sm:text-3xl",
        )}
      >
        {title}
      </Heading>
      {description ? <div className="mt-3 text-lg text-ink-600">{description}</div> : null}
    </div>
  );
}
