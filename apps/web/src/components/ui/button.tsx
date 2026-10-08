import Link from "next/link";
import type { ButtonHTMLAttributes, ComponentProps } from "react";

import { cx } from "@/lib/cx";

export type ButtonVariant = "primary" | "secondary" | "outline" | "inverse" | "ghost";
export type ButtonSize = "md" | "lg";

const base =
  "inline-flex items-center justify-center gap-2 rounded-full font-semibold transition-colors " +
  "disabled:cursor-not-allowed disabled:opacity-60 min-h-11"; // 44px touch target (FRONTEND.md §6)

const variants: Record<ButtonVariant, string> = {
  primary: "bg-brand-700 text-white hover:bg-brand-800",
  secondary: "bg-gold-400 text-ink-900 hover:bg-gold-500",
  outline: "border-2 border-brand-700 text-brand-700 hover:bg-brand-50",
  inverse: "border-2 border-white text-white hover:bg-white/10", // on dark (brand-900) surfaces
  ghost: "text-brand-700 hover:bg-brand-50",
};

const sizes: Record<ButtonSize, string> = {
  md: "px-5 py-2 text-base",
  lg: "px-6 py-3 text-lg",
};

export function buttonClasses(variant: ButtonVariant = "primary", size: ButtonSize = "md", className?: string) {
  return cx(base, variants[variant], sizes[size], className);
}

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

export function Button({ variant, size, className, type = "button", ...props }: ButtonProps) {
  return <button type={type} className={buttonClasses(variant, size, className)} {...props} />;
}

export interface ButtonLinkProps extends ComponentProps<typeof Link> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

/** A navigation link styled as a button (navigation must stay a link for assistive tech). */
export function ButtonLink({ variant, size, className, ...props }: ButtonLinkProps) {
  return <Link className={buttonClasses(variant, size, className)} {...props} />;
}
