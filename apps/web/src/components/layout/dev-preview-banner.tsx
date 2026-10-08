/** Persistent, honest notice that this is not a live fundraising service. */
export function DevPreviewBanner() {
  return (
    <div className="bg-gold-400 text-ink-900">
      <p className="mx-auto max-w-6xl px-4 py-2 text-center text-sm font-semibold sm:px-6 lg:px-8">
        Development preview — FundZim is not open for fundraising. No donations or payments can be made.
      </p>
    </div>
  );
}
