// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import type { Group, Monitor, Status } from './api.ts';
import { buildTree, canDrop, findGroup, flatten, groupChoices, groupStatus, key, layout, members, move } from './tree.ts';

const group = (id: number, name: string, parent: number | null = null, position = 0): Group => ({
  id,
  name,
  parent_id: parent,
  position,
  alert: 'none',
  channels: [],
  alert_monitors: [],
  created_at: '',
  updated_at: '',
});

const monitor = (id: number, name: string, groupID: number | null = null, position = 0, status: Status = 'up', active = true) =>
  ({ id, name, group_id: groupID, position, active, status: { status } }) as Monitor;

// Site (1) holds Web (m10) and Shop (2), which holds Cart (m11); DB (m12) is at the top.
const groups = [group(2, 'Shop', 1, 1), group(1, 'Site')];
const monitors = [monitor(12, 'DB'), monitor(11, 'Cart', 2), monitor(10, 'Web', 1, 0)];
const keys = (list: { item: ReturnType<typeof buildTree>[number] }[]) => list.map((r) => key(r.item));

describe('buildTree', () => {
  it('orders by position, groups first, then by name', () => {
    const tree = buildTree(groups, monitors);
    assert.deepEqual(keys(flatten(tree, new Set())), ['g1', 'm10', 'g2', 'm11', 'm12']);
    assert.deepEqual(
      flatten(tree, new Set()).map((r) => [r.depth, r.size]),
      [
        [0, 2],
        [1, 0],
        [1, 1],
        [2, 0],
        [0, 0],
      ],
    );
  });
  it('hides collapsed groups and keeps orphans', () => {
    const tree = buildTree(groups, [...monitors, monitor(13, 'Lost', 99)]);
    assert.deepEqual(keys(flatten(tree, new Set([1]))), ['g1', 'm12', 'm13']);
  });
  it('shows groups in a loop at the top', () => {
    const tree = buildTree([group(1, 'A', 2), group(2, 'B', 1)], []);
    assert.deepEqual(
      tree.map((it) => key(it)),
      ['g1', 'g2'],
    );
  });
});

describe('groupStatus', () => {
  it('sums up the active members, nested too', () => {
    const tree = buildTree(groups, [monitor(10, 'Web', 1), monitor(11, 'Cart', 2, 0, 'down'), monitor(14, 'Off', 2, 0, 'down', false)]);
    const site = findGroup(tree, 1);
    if (!site) throw new Error('no Site');
    assert.deepEqual(groupStatus(site), { status: 'down', up: 1, down: 1, total: 2 });
    assert.equal(members(site).length, 3);
    const one = (s: Status) => groupStatus(buildTree([group(1, 'G')], [monitor(1, 'M', 1, 0, s)])[0]).status;
    assert.equal(one('pending'), 'pending');
    assert.equal(one('maintenance'), 'maintenance');
    assert.equal(one('unknown'), 'unknown');
    assert.equal(findGroup(tree, 99), null);
  });
});

describe('move', () => {
  const tree = buildTree(groups, monitors);
  const order = (t: ReturnType<typeof buildTree>) => keys(flatten(t, new Set()));
  it('moves before, after and inside', () => {
    assert.deepEqual(order(move(tree, 'm12', { target: 'g1', where: 'before' })), ['m12', 'g1', 'm10', 'g2', 'm11']);
    assert.deepEqual(order(move(tree, 'm10', { target: 'm11', where: 'after' })), ['g1', 'g2', 'm11', 'm10', 'm12']);
    assert.deepEqual(order(move(tree, 'm12', { target: 'g2', where: 'inside' })), ['g1', 'm10', 'g2', 'm11', 'm12']);
    assert.deepEqual(order(move(tree, 'g2', { target: '', where: 'inside' })), ['g1', 'm10', 'm12', 'g2', 'm11']);
    // The original is unchanged.
    assert.deepEqual(order(tree), ['g1', 'm10', 'g2', 'm11', 'm12']);
  });
  it('refuses impossible moves', () => {
    for (const [dragged, target, where] of [
      ['g1', 'g2', 'inside'],
      ['g1', 'm11', 'before'],
      ['g1', 'g1', 'inside'],
      ['m12', 'm10', 'inside'],
      ['m99', 'g1', 'inside'],
      ['m12', 'm99', 'before'],
      ['m12', '', 'before'],
    ] as const) {
      assert.equal(canDrop(tree, dragged, { target, where }), false, `${dragged} ${where} ${target}`);
      assert.equal(move(tree, dragged, { target, where }), tree);
    }
  });
});

describe('layout', () => {
  it('lists every item with its group', () => {
    assert.deepEqual(layout(buildTree(groups, monitors)), [
      { type: 'group', id: 1, group_id: null },
      { type: 'monitor', id: 10, group_id: 1 },
      { type: 'group', id: 2, group_id: 1 },
      { type: 'monitor', id: 11, group_id: 2 },
      { type: 'monitor', id: 12, group_id: null },
    ]);
  });
  it('offers groups to choose, without a group and those inside it', () => {
    const tree = buildTree(groups, monitors);
    assert.deepEqual(
      groupChoices(tree).map((c) => [c.group.id, c.depth]),
      [
        [1, 0],
        [2, 1],
      ],
    );
    assert.deepEqual(groupChoices(tree, 1), []);
    assert.deepEqual(
      groupChoices(tree, 2).map((c) => c.group.id),
      [1],
    );
  });
});
