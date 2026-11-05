// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { blocks, inline } from './markdown.ts';

describe('markdown', () => {
  it('reads blocks', () => {
    const md = "## What's new\r\n\r\n- Faster checks\n- `eyrie agent` on ARM\n\n1. One\n2. Two\n\nA paragraph\nthat wraps.\n\n```\ncode <b>\n```\n### Done ##";
    assert.deepEqual(blocks(md), [
      { kind: 'heading', level: 2, text: [{ kind: 'text', text: "What's new" }] },
      {
        kind: 'list',
        ordered: false,
        items: [
          [{ kind: 'text', text: 'Faster checks' }],
          [
            { kind: 'code', text: 'eyrie agent' },
            { kind: 'text', text: ' on ARM' },
          ],
        ],
      },
      { kind: 'list', ordered: true, items: [[{ kind: 'text', text: 'One' }], [{ kind: 'text', text: 'Two' }]] },
      { kind: 'paragraph', text: [{ kind: 'text', text: 'A paragraph that wraps.' }] },
      { kind: 'code', text: 'code <b>' },
      { kind: 'heading', level: 3, text: [{ kind: 'text', text: 'Done' }] },
    ]);
    assert.deepEqual(blocks(''), []);
  });
  it('reads inline parts', () => {
    assert.deepEqual(inline('**Full Changelog**: https://github.com/example/eyrie/compare/v1...v2.'), [
      { kind: 'strong', text: 'Full Changelog' },
      { kind: 'text', text: ': ' },
      { kind: 'link', text: 'https://github.com/example/eyrie/compare/v1...v2', href: 'https://github.com/example/eyrie/compare/v1...v2' },
      { kind: 'text', text: '.' },
    ]);
    assert.deepEqual(inline('see [the docs](https://example.com/docs) or [this](javascript:alert(1))'), [
      { kind: 'text', text: 'see ' },
      { kind: 'link', text: 'the docs', href: 'https://example.com/docs' },
      { kind: 'text', text: ' or [this](javascript:alert(1))' },
    ]);
  });
});
