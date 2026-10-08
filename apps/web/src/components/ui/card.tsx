import type { HTMLAttributes } from "react";

import { cx } from "@/lib/cx";

type CardElement = "div" | "article" | "li" | "section";

export interface CardProps extends HTMLAttributes<HTMLElement> {
  as?: CardElement;
}

export function Card({ as: Element = "div", className, ...props }: CardProps) {
  return (
    <Element
      className={cx("rounded-card border border-line bg-surface p-6 shadow-card", className)}
      {...props}
    />
  );
}
