// SPDX-License-Identifier: AGPL-3.0-or-later
// The monitor list as a tree of groups and monitors, and moving things in
// it. Pure functions, so the tests can run them.
import type { Group, LayoutItem, Monitor, Status } from './api.ts';

export type Item = { kind: 'group'; group: Group; children: Item[] } | { kind: 'monitor'; monitor: Monitor };

/** An item's key: g<id> for a group, m<id> for a monitor. */
export function key(it: Item): string {
  return it.kind === 'group' ? `g${it.group.id}` : `m${it.monitor.id}`;
}

const nameOf = (it: Item) => (it.kind === 'group' ? it.group.name : it.monitor.name);
const positionOf = (it: Item) => (it.kind === 'group' ? it.group.position : it.monitor.position);

/**
 * The tree: in each group, its groups and monitors by position; at the
 * same position groups first, then by name. Monitors and groups whose
 * group is missing go to the top level.
 */
export function buildTree(groups: Group[], monitors: Monitor[]): Item[] {
  const ids = new Set(groups.map((g) => g.id));
  const byParent = new Map<number, Item[]>();
  const add = (parent: number | null, it: Item) => {
    const p = parent !== null && ids.has(parent) ? parent : 0;
    byParent.set(p, [...(byParent.get(p) ?? []), it]);
  };
  const items = new Map<number, Item & { kind: 'group' }>();
  for (const g of groups) {
    const it: Item & { kind: 'group' } = { kind: 'group', group: g, children: [] };
    items.set(g.id, it);
    add(g.parent_id, it);
  }
  for (const m of monitors) add(m.group_id, { kind: 'monitor', monitor: m });
  const sort = (list: Item[]) =>
    list.sort(
      (a, b) =>
        positionOf(a) - positionOf(b) ||
        (a.kind === b.kind ? 0 : a.kind === 'group' ? -1 : 1) ||
        nameOf(a).localeCompare(nameOf(b)),
    );
  for (const [id, it] of items) it.children = sort(byParent.get(id) ?? []);
  // A group in a loop (it should not happen) is not reached from the top:
  // show it there.
  const root = sort(byParent.get(0) ?? []);
  const reached = new Set<number>();
  const walk = (list: Item[]) => {
    for (const it of list) {
      if (it.kind === 'group' && !reached.has(it.group.id)) {
        reached.add(it.group.id);
        walk(it.children);
      }
    }
  };
  walk(root);
  for (const [id, it] of items) if (!reached.has(id) && !root.includes(it)) root.push(it);
  return root;
}

/** Every monitor in a group and the groups inside it. */
export function members(it: Item): Monitor[] {
  if (it.kind === 'monitor') return [it.monitor];
  return it.children.flatMap(members);
}

/** A group's status from its active members: the worst of down, pending, up and maintenance. */
export function groupStatus(it: Item): { status: Status; up: number; down: number; total: number } {
  const ms = members(it).filter((m) => m.active);
  const count = (s: Status) => ms.filter((m) => m.status.status === s).length;
  const up = count('up');
  const down = count('down');
  let status: Status = 'unknown';
  if (down) status = 'down';
  else if (count('pending')) status = 'pending';
  else if (up) status = 'up';
  else if (count('maintenance')) status = 'maintenance';
  return { status, up, down, total: ms.length };
}

/** A visible row of the list: an item, how deep it is and how many items are directly in it. */
export interface Row {
  item: Item;
  depth: number;
  size: number;
}

/** The rows shown, without the contents of collapsed groups. */
export function flatten(tree: Item[], collapsed: Set<number>): Row[] {
  const out: Row[] = [];
  const walk = (list: Item[], depth: number) => {
    for (const it of list) {
      out.push({ item: it, depth, size: it.kind === 'group' ? it.children.length : 0 });
      if (it.kind === 'group' && !collapsed.has(it.group.id) && depth < 20) walk(it.children, depth + 1);
    }
  };
  walk(tree, 0);
  return out;
}

/** Where a dragged item goes: before or after the item with key target, or inside its group (at the end). Target '' is the top level. */
export interface Drop {
  target: string;
  where: 'before' | 'after' | 'inside';
}

function find(tree: Item[], k: string): { list: Item[]; index: number } | null {
  for (let i = 0; i < tree.length; i++) {
    const it = tree[i];
    if (key(it) === k) return { list: tree, index: i };
    if (it.kind === 'group') {
      const r = find(it.children, k);
      if (r) return r;
    }
  }
  return null;
}

function contains(it: Item, k: string): boolean {
  return it.kind === 'group' && it.children.some((c) => key(c) === k || contains(c, k));
}

/** Whether dragged can go there: not onto itself, and a group not into itself or a group inside it. */
export function canDrop(tree: Item[], dragged: string, drop: Drop): boolean {
  if (drop.target === dragged) return false;
  const from = find(tree, dragged);
  if (!from) return false;
  if (drop.target === '') return drop.where === 'inside';
  const to = find(tree, drop.target);
  if (!to) return false;
  if (drop.where === 'inside' && to.list[to.index].kind !== 'group') return false;
  return !contains(from.list[from.index], drop.target);
}

function clone(tree: Item[]): Item[] {
  return tree.map((it) => (it.kind === 'group' ? { ...it, children: clone(it.children) } : it));
}

/** The tree with dragged moved to drop; the same tree when it cannot go there. */
export function move(tree: Item[], dragged: string, drop: Drop): Item[] {
  if (!canDrop(tree, dragged, drop)) return tree;
  const out = clone(tree);
  const from = find(out, dragged);
  if (!from) return tree;
  const [it] = from.list.splice(from.index, 1);
  if (drop.target === '') {
    out.push(it);
    return out;
  }
  const to = find(out, drop.target);
  if (!to) return tree;
  const target = to.list[to.index];
  if (drop.where === 'inside' && target.kind === 'group') target.children.push(it);
  else to.list.splice(drop.where === 'before' ? to.index : to.index + 1, 0, it);
  return out;
}

/** Every item with the group it is in, in order: what PUT /api/v1/layout takes. */
export function layout(tree: Item[]): LayoutItem[] {
  const out: LayoutItem[] = [];
  const walk = (list: Item[], parent: number | null) => {
    for (const it of list) {
      if (it.kind === 'group') {
        out.push({ type: 'group', id: it.group.id, group_id: parent });
        walk(it.children, it.group.id);
      } else {
        out.push({ type: 'monitor', id: it.monitor.id, group_id: parent });
      }
    }
  };
  walk(tree, null);
  return out;
}

/** The groups, depth first, each with its depth: for choosing one in a list. A group and those inside it can be left out. */
export function groupChoices(tree: Item[], without = 0): { group: Group; depth: number }[] {
  const out: { group: Group; depth: number }[] = [];
  const walk = (list: Item[], depth: number) => {
    for (const it of list) {
      if (it.kind !== 'group' || it.group.id === without) continue;
      out.push({ group: it.group, depth });
      walk(it.children, depth + 1);
    }
  };
  walk(tree, 0);
  return out;
}

/** The group with this ID in the tree, as an item. */
export function findGroup(tree: Item[], id: number): (Item & { kind: 'group' }) | null {
  const r = find(tree, `g${id}`);
  const it = r?.list[r.index];
  return it?.kind === 'group' ? it : null;
}
