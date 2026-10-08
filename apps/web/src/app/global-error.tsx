"use client";

import "./globals.css";

/**
 * Replaces the root layout when the layout itself fails. Must render its own <html>/<body> and cannot
 * export metadata (React <title> is used instead).
 */
export default function GlobalError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return (
    <html lang="en-ZW">
      <body className="bg-canvas text-ink-900">
        <title>Something went wrong · FundZim</title>
        <main className="mx-auto max-w-xl px-4 py-24">
          <div role="alert" className="rounded-card border border-danger-700/30 bg-danger-50 p-6">
            <h1 className="font-display text-2xl font-semibold text-danger-700">FundZim is having a problem</h1>
            <p className="mt-2 text-ink-700">Please try again in a moment.</p>
            {error.digest ? (
              <p className="mt-3 text-sm text-ink-600">
                Reference: <code className="font-mono">{error.digest}</code>
              </p>
            ) : null}
            <button
              type="button"
              onClick={() => retry()}
              className="mt-4 inline-flex min-h-11 items-center rounded-full bg-brand-700 px-5 font-semibold text-white"
            >
              Try again
            </button>
          </div>
        </main>
      </body>
    </html>
  );
}
