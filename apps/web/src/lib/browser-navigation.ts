/**
 * Full-page navigation for auth transitions (sign in/out, session expiry). A full load guarantees that
 * every Server Component re-renders with the new cookies and that no signed-in UI lingers in the client
 * router cache. Wrapped so unit tests can observe it (jsdom does not implement navigation).
 */
export const browserNavigation = {
  assign(url: string): void {
    window.location.assign(url);
  },
  /** Current path + query, for `?next=` after a session expires. */
  currentPath(): string {
    return `${window.location.pathname}${window.location.search}`;
  },
  /** Removes secrets (tokens) from the address bar and history without reloading. */
  replaceUrl(url: string): void {
    window.history.replaceState(window.history.state, "", url);
  },
};
