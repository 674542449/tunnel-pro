// Test fixtures and report names follow the same product release as the binaries.
import {readFileSync} from 'node:fs';
export const productVersion = readFileSync(new URL('../VERSION', import.meta.url), 'utf8').trim();
if (!/^v\d+\.\d+\.\d+$/.test(productVersion)) throw Error('Invalid VERSION');
