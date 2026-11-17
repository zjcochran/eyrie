// SPDX-License-Identifier: AGPL-3.0-or-later
// A dialog for keyboard and screen reader users: focus moves into it when
// it opens, Tab and Shift+Tab stay inside it while it is open, and focus
// goes back where it was when it closes. Use it as an action: <div role="dialog" use:modal>.

const focusable =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';

export function modal(node: HTMLElement) {
  const before = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const key = (e: KeyboardEvent) => {
    if (e.key !== 'Tab') return;
    const all = [...node.querySelectorAll<HTMLElement>(focusable)].filter((el) => el.offsetParent !== null);
    if (!all.length) return;
    const first = all[0];
    const last = all[all.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  };
  node.addEventListener('keydown', key);
  queueMicrotask(() => {
    if (node.contains(document.activeElement)) return;
    const first = node.querySelector<HTMLElement>(focusable);
    if (first) first.focus();
    else {
      node.tabIndex = -1;
      node.focus();
    }
  });
  return {
    destroy() {
      node.removeEventListener('keydown', key);
      // Unless the dialog took the page elsewhere and focus with it.
      if (before?.isConnected) queueMicrotask(() => before.focus());
    },
  };
}
