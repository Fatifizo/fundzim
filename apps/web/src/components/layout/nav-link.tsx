"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, type ComponentProps } from "react";

/** `exact`: only the path itself is current, not its sub-paths (for an "Overview" entry above its children). */
type NavLinkProps = ComponentProps<typeof Link> & { href: string; exact?: boolean };

function CurrentAwareLink({ href, exact = false, ...props }: NavLinkProps) {
  const pathname = usePathname();
  const current = pathname === href || (!exact && href !== "/" && pathname?.startsWith(`${href}/`));
  return <Link href={href} aria-current={current ? "page" : undefined} {...props} />;
}

/**
 * Link that marks itself `aria-current="page"` on the matching route. On dynamic routes the pathname is
 * only known at request time (cacheComponents), so a plain link renders in the static shell and the
 * current-aware link streams in.
 */
export function NavLink({ exact, ...props }: NavLinkProps) {
  return (
    <Suspense fallback={<Link {...props} />}>
      <CurrentAwareLink exact={exact} {...props} />
    </Suspense>
  );
}
