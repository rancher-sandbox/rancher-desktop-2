import 'expect';

declare module 'expect' {
  interface AsymmetricMatchers {
    /**
     * Asserts that the received value is one of the expected values.
     */
    toBeOneOf(...expected: any[]): void;
  }
  interface Matchers<R> {
    /**
     * Asserts that the received value is one of the expected values.
     */
    toBeOneOf(...expected: any[]): R;
  }
}
