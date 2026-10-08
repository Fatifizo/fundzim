import Link from "next/link";
import { Suspense } from "react";

import { ApiStatus } from "@/components/api-status";
import { Logo } from "@/components/logo";
import { FOOTER_NAV } from "@/lib/navigation";

export function SiteFooter() {
  return (
    <footer className="on-dark mt-auto bg-brand-900 text-white">
      <div className="mx-auto grid max-w-6xl gap-8 px-4 py-12 sm:px-6 md:grid-cols-[1.5fr_1fr] lg:px-8">
        <div>
          <Logo inverted />
          <p className="mt-3 max-w-md text-brand-100">Fund anyone in Zimbabwe, from anywhere.</p>
          <p className="mt-3 max-w-md text-sm text-brand-100">
            FundZim is under development and is not yet accepting campaigns, donations or payments.
          </p>
        </div>
        <nav aria-label="Footer">
          <ul className="grid grid-cols-2 gap-x-6 gap-y-1">
            {FOOTER_NAV.map((item) => (
              <li key={item.href}>
                <Link
                  href={item.href}
                  className="inline-flex min-h-11 items-center text-white underline-offset-4 hover:text-gold-400 hover:underline"
                >
                  {item.label}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
      </div>
      <div className="border-t border-white/15">
        <div className="mx-auto flex max-w-6xl flex-col gap-2 px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-6 lg:px-8">
          <p className="text-sm text-brand-100">Development preview · not open for fundraising</p>
          <Suspense fallback={<p className="text-sm text-brand-100">Platform API: checking…</p>}>
            <ApiStatus />
          </Suspense>
        </div>
      </div>
    </footer>
  );
}
