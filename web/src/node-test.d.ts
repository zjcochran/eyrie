// SPDX-License-Identifier: AGPL-3.0-or-later
// Types for the parts of Node.js's built-in test runner the tests use, so
// the project needs no @types/node.
declare module 'node:test' {
  type Body = () => void | Promise<void>;
  export function describe(name: string, fn: Body): void;
  export function it(name: string, fn: Body): void;
}

declare module 'node:assert/strict' {
  interface Assert {
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    rejects(fn: Promise<unknown> | (() => Promise<unknown>), expected?: RegExp, message?: string): Promise<void>;
  }
  const assert: Assert;
  export default assert;
}
