/**
 * Jest preloads this file into every test file (see jest.config.js,
 * `setupFilesAfterEnv`) so we can have additional matchers.
 */

import { expect } from '@jest/globals';

expect.extend({
  toBeOneOf: function(actual, ...expected) {
    const actualString = this.utils.printReceived(actual);
    const expectedString = this.utils.printExpected(expected);
    return {
      pass:    expected.some(value => this.equals(actual, value, this.customTesters)),
      message: () =>
        this.isNot
          ? `expected ${ actualString } not to be one of ${ expectedString }`
          : `expected ${ actualString } to be one of ${ expectedString }`,
    };
  },
});
