// This is a transformer for Vue to compile single-file components in jest.
// @vue/vue3-jest forces CommonJS which breaks with dependencies that are now
// ESM-only.

// @ts-check

import crypto from 'node:crypto';
import fs from 'node:fs';

import typescript from 'typescript';
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc';

/**
 * @import { SFCDescriptor, SFCParseOptions, SFCScriptBlock } from 'vue/compiler-sfc'
 * @import { SyncTransformer } from '@jest/transform'
 * @import { TransformOptions as BabelTransformOptions } from '@babel/core';
 */

/**
 * @typedef {Object} VueJestTransformOptions
 * @property vue {SFCParseOptions}
 * @property babel {BabelTransformOptions}
 */

/** @type (source: string, fileName: string) => string */
function compileTypeScript(source, fileName) {
  const result = typescript.transpileModule(source, {
    fileName,
    compilerOptions: {
      module: typescript.ModuleKind.ESNext,
      jsx:    typescript.JsxEmit.Preserve,
    },
  });

  return result.outputText;
}

/** @type (descriptor: SFCDescriptor, compiledScript: SFCScriptBlock | undefined) => string */
function processScript(descriptor, compiledScript) {
  if (!descriptor.script && !descriptor.scriptSetup) {
    // No script content; export something for the template.
    return 'const __default__ = {};';
  }

  let content = compiledScript?.content ?? descriptor.script?.content ?? '';
  if (!content) {
    throw new Error(`Script ${ descriptor.filename } has no content`);
  }

  const lang = descriptor.scriptSetup?.lang ?? descriptor.script?.lang ?? 'js';
  const isTS = /typescript|^ts/.test(lang);

  if (isTS) {
    content = compileTypeScript(content, descriptor.filename);
  }

  const exportExpr = /^export default/m;
  if (exportExpr.test(content)) {
    return content.replace(exportExpr, 'const __default__ =');
  }
  return content + '\nconst __default__ = {};';
}

/** @type (descriptor: SFCDescriptor, compiledScript: SFCScriptBlock | undefined) => string */
function processTemplate(descriptor, compiledScript) {
  const { template } = descriptor;

  if (!template) {
    return '';
  }

  if (!template.content) {
    throw new Error(`Template ${ descriptor.filename } does not have content`);
  }

  const lang = descriptor.scriptSetup?.lang ?? descriptor.script?.lang ?? 'js';
  const isTS = /typescript|^ts/.test(lang);
  const results = compileTemplate({
    source:          template.content,
    ast:             template.ast,
    filename:        descriptor.filename,
    id:              descriptor.filename,
    compilerOptions: {
      mode:            'module',
      isTS,
      bindingMetadata: compiledScript?.bindings,
    },
    preprocessLang: template.lang,
  });

  if (isTS) {
    return compileTypeScript(results.code, descriptor.filename);
  }

  return results.code;
}

/**
 * The source of this file; used to ensure the cache key changes if this is
 * updated.
 */
const VueJestScript = fs.readFileSync(new URL(import.meta.url));

/** @type {SyncTransformer<VueJestTransformOptions>} */
export default {
  getCacheKey(sourceText, sourcePath, options) {
    const sourceHasher = crypto.createHash('sha512');

    sourceHasher.update(VueJestScript);
    sourceHasher.update(sourceText, 'utf-8');

    return sourceHasher.digest('hex') + sourcePath;
  },

  process(sourceText, filename, options) {
    const { descriptor } = parse(sourceText, { filename, ...options.transformerConfig });

    /** @type { SFCScriptBlock | undefined } */
    let compiledScript;
    if (descriptor.scriptSetup) {
      compiledScript = compileScript(descriptor, { id: filename });
    }

    const code = `
      ${ processScript(descriptor, compiledScript) }
      ${ processTemplate(descriptor, compiledScript) }
      /* Don't bother with styles, we don't need it yet */
      ${ descriptor.template ? '__default__.render = render;' : '' }
      export default __default__;
    `;

    return { code };
  },
};
