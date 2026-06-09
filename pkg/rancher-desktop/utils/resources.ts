import memoize from 'lodash/memoize';

/**
 * executableMap is a mapping of valid executable names and their path.
 * If the value is `undefined`, then it's assumed to be an executable in the
 * user-accessible `bin` directory.
 * Otherwise, it's an array containing the path to the executable.
 */
const executableMap = {
  'wsl-helper': undefined,
} satisfies Record<string, string | undefined>;

function platformBinary(name: string): string {
  return process.platform === 'win32' ? `${ name }.exe` : name;
}

/**
 * Gets the absolute path to an executable. Adds ".exe" to the end
 * if running on Windows.
 * @param name The name of the binary, without file extension.
 */
function _executable(name: keyof typeof executableMap): string {
  throw new Error(`All executables are no longer implemented`);
}
export const executable = memoize(_executable);

export default { executable };
