// Partial<T> (https://www.typescriptlang.org/docs/handbook/utility-types.html#partialtype)
// only allows missing properties on the top level; if anything is given, then all
// properties of that top-level property must exist.  RecursivePartial<T> instead
// allows any descendent properties to be omitted.
export type RecursivePartial<T> = {
  [P in keyof T]?:
  T[P] extends (infer U)[] ? RecursivePartial<U>[] :
    T[P] extends object ? RecursivePartial<T[P]> :
      T[P];
};

export type RecursiveReadonly<T> = {
  readonly [P in keyof T]:
  T[P] extends (infer U)[] ? readonly RecursiveReadonly<U>[] :
    T[P] extends object ? RecursiveReadonly<T[P]> :
      T[P];
};

/**
 * Converts a union type to an intersection type.
 * `A | B | C` becomes `A & B & C`.
 */
export type UnionToIntersection<U> =
  (U extends any ? (k: U) => void : never) extends ((k: infer I) => void) ? I : never;

export type ReadWrite<T> = {
  -readonly [P in keyof T]: T[P];
};

/** UpperAlpha is the set of upper-case alphabets. */
type UpperAlpha =
  'A' | 'B' | 'C' | 'D' | 'E' | 'F' | 'G' | 'H' | 'I' | 'J' | 'K' | 'L' | 'M' |
  'N' | 'O' | 'P' | 'Q' | 'R' | 'S' | 'T' | 'U' | 'V' | 'W' | 'X' | 'Y' | 'Z';

/** Alpha is the set of upper- or lower-case alphabets. */
type Alpha<T> = T extends UpperAlpha ? T : T extends Lowercase<UpperAlpha> ? T : never;

type UpperSnakeCaseInner<T extends string> =
  T extends '' ? never :
    T extends UpperAlpha ? `_${ T }` :
      T extends Alpha<T> ? Uppercase<T> :
        T extends `${ infer C }${ infer U }` ? `${ UpperSnakeCaseInner<C> }${ UpperSnakeCaseInner<U> }` :
          never;

/**
 * UpperSnakeCase transforms a string into upper snake case (all uppercase,
 * underscore word separators.
 *
 * @example UpperSnakeCase<'HelloWorld'> == 'HELLO_WORLD'
 * @note This fails if there are any non-alphabetic characters.
 */
export type UpperSnakeCase<T extends string | symbol | number > =
  T extends symbol | number ? never :
    T extends Alpha<T> ? Uppercase<T> :
      T extends `${ infer C }${ infer U }` ? `${ Uppercase<C> }${ UpperSnakeCaseInner<U> }`
        : T;

export function UpperSnakeCase<T extends string>(input: T): UpperSnakeCase<T> {
  return input.replace(/./g, (c, i) => (i > 0 && /[A-Z]/.test(c) ? '_' : '') + c.toUpperCase()) as UpperSnakeCase<T>;
}

/**
 * RecursiveKeys returns the set of all keys of a type, recursively, separated
 * by dots.
 *
 * @example RecursiveKeys<{a: { b: number}, c: number}> = 'a' | 'a.b' | 'c'
 */
export type RecursiveKeys<T> =
  Record<string, unknown> extends T ? string :
    T extends readonly unknown[] ? RecursiveKeys<T[number]> :
      T extends object ? keyof T & string | RecursiveKeysInner<Required<T>, keyof T & string> :
        never;

type RecursiveKeysInner<T, K extends string> = K extends keyof T ? `${ K }.${ RecursiveKeys<T[K]> }` : never;

/**
 * RecursiveLeafKeys returns the set of all keys of a type, recursively, without
 * any intermediate keys, separated by dots.
 *
 * @example RecursiveLeafKeys<{a: { b: number}, c: number}> = 'a.b' | 'c'
 * @note This currently does not handle arrays.
 */
export type RecursiveLeafKeys<T> =
  T extends readonly unknown[] ? RecursiveLeafKeys<T[number]> :
    T extends object ?
      RecursiveLeafKeysInner<Required<T>, keyof T & string> :
      never;

type RecursiveLeafKeysInner<T, K extends string> =
  K extends keyof T ?
    NonNullable<T[K]> extends object ?
      // `0 extends 1 & ...` checks if `...` is `any`, avoiding `any extends ...`
      // because `any` is special and causes both branches to be taken.
      0 extends 1 & NonNullable<T[K]> ? never :
      `${ K }.${ RecursiveLeafKeys<T[K]> }` :
      `${ K }` :
    never;

/**
 * Get the type of a value at a given key in a nested object type.
 * @example FieldType<{ a: { b: number } }, 'a.b'> = number
 * @note This currently does not handle arrays.
 */
export type FieldType<T, K extends RecursiveLeafKeys<T>> =
  K extends `${ infer P }.${ infer R }` ?
    P extends keyof T ?
      FieldType<NonNullable<T[P]>, R & RecursiveLeafKeys<T[P]>> :
      never :
    K extends keyof T ? T[K] : never;

/**
 * RecursiveTypes returns a single-level type mapping of RecursiveKeys<T> to
 * the value type in T.
 */
export type RecursiveTypes<T extends Record<string, any>> =
  Record<string, unknown> extends T ? never :
    {
      [P in RecursiveKeys<T>]:
      P extends keyof T ?
        T[P] :
        P extends `${ infer K }.${ infer R }` ?
          (
            K extends keyof T ?
              (
                T[K] extends Record<string, unknown> ?
                  ( R extends keyof RecursiveTypes<T[K]> ? RecursiveTypes<T[K]>[R] : never ) :
                  never
              ) :
              never
          ) :
          never;
    };

/**
 * Check if a given object is defined (i.e. not undefined, and not null).
 */
export function defined<T>(input: T | undefined | null): input is T {
  return typeof input !== 'undefined' && input !== null;
}

export type Direction = 'back' | 'forward';
