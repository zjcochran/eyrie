// Where the app is: the browser's path and query, kept in sync with the
// history so the back button works.

export const nav = $state({ path: location.pathname, search: location.search });

function sync() {
  nav.path = location.pathname;
  nav.search = location.search;
}

window.addEventListener('popstate', sync);

/** Goes to a path of the app without reloading the page. */
export function go(path: string, replace = false) {
  if (replace) {
    history.replaceState(null, '', path);
  } else {
    history.pushState(null, '', path);
  }
  sync();
  window.scrollTo(0, 0);
}

/** Follows a click on a local link through the app. */
export function link(event: MouseEvent) {
  const a = event.currentTarget as HTMLAnchorElement;
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
    return;
  }
  event.preventDefault();
  go(a.getAttribute('href') ?? '/');
}
