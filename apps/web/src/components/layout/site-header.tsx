import Link from "next/link";
import { Suspense } from "react";

import { Logo } from "@/components/logo";
import { PRIMARY_NAV } from "@/lib/navigation";

import { HeaderAccount, HeaderAccountFallback } from "./header-account";
import { MobileNav } from "./mobile-nav";
import { NavLink } from "./nav-link";

export function SiteHeader() {
  return (
    <header className="sticky top-0 z-40 border-b border-line bg-canvas/95 backdrop-blur supports-[backdrop-filter]:bg-canvas/85">
      <div className="relative mx-auto flex min-h-16 max-w-6xl items-center justify-between gap-4 px-4 sm:px-6 lg:px-8">
        <Link href="/" aria-label="FundZim home" className="rounded-lg py-2">
          <Logo />
        </Link>
        <nav aria-label="Main" className="hidden md:block">
          <ul className="flex items-center gap-1">
            {PRIMARY_NAV.map((item) => (
              <li key={item.href}>
                <NavLink
                  href={item.href}
                  className={
                    item.href === "/start"
                      ? "ml-2 inline-flex min-h-11 items-center rounded-full bg-brand-700 px-5 font-semibold text-white hover:bg-brand-800"
                      : "inline-flex min-h-11 items-center rounded-full px-4 font-medium text-ink-700 hover:bg-brand-50 hover:text-brand-800 aria-[current=page]:text-brand-700 aria-[current=page]:underline underline-offset-4"
                  }
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>
        <div className="flex items-center gap-2">
          <Suspense fallback={<HeaderAccountFallback />}>
            <HeaderAccount />
          </Suspense>
          <MobileNav items={PRIMARY_NAV} />
        </div>
      </div>
    </header>
  );
}
