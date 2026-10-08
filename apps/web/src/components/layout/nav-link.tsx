"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, type ComponentProps } from "react";

type NavLinkProps = ComponentProps<typeof Link> & { href: string };

function CurrentAwareLink({ href, ...props }: NavLinkProps) {
  const pathname = usePathname();
  const current = pathname === href || (href !== "/" && pathname?.startsWith(`${href}/`));
  return <Link href={href} aria-current={current ? "page" : undefined} {...props} />;
}

/**
 * Link that marks itself `aria-current="page"` on the matching route. On dynamic routes the pathname is
 * only known at request time (cacheComponents), so a plain link renders in the static shell and the
 * current-aware link streams in.
 */
export function NavLink(props: NavLinkProps) {
  return (
    <Suspense fallback={<Link {...props} />}>
      <CurrentAwareLink {...props} />
    </Suspense>
  );
}
