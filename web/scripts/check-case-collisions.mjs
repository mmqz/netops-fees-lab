#!/usr/bin/env node
/**
 * Case-collision gate (#22) — fail when one directory holds two file names
 * that differ ONLY by letter case.
 *
 * Why: Linux/macOS file systems are case-sensitive so both files can coexist
 * and every gate stays green there; Windows (and macOS default HFS+ mounts)
 * are case-INSENSITIVE — the second file overwrites/unshadows the first and
 * `tsc`/`vite` die with TS1261 / "not exported by" (#22's exact failure).
 * The gate exists so the collision dies in CI on every platform, not on a
 * Windows laptop.
 *
 * Usage: node scripts/check-case-collisions.mjs [dirs...]   (default: src public)
 * Exit 0 = clean, exit 1 = collision list printed.
 */

import { readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const DEFAULT_DIRS = ['src', 'public'];
const roots = process.argv.length > 2 ? process.argv.slice(2) : DEFAULT_DIRS;
const cwd = process.cwd();

/** Collect (relative dir, file name) pairs recursively, skipping build/vendor noise. */
function walk(dir, out) {
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch {
    return; // absent dir (e.g. no public/) is not a collision
  }
  for (const e of entries) {
    if (e.name === 'node_modules' || e.name === 'dist') continue;
    const full = join(dir, e.name);
    if (e.isDirectory()) {
      walk(full, out);
    } else if (e.isFile()) {
      out.push({ dir: dir === cwd ? '.' : dir, name: e.name });
    }
  }
}

const files = [];
for (const root of roots) {
  const abs = join(cwd, root);
  if (!statSync(abs, { throwIfNoEntry: false })?.isDirectory()) continue;
  walk(abs, files);
}

// Group by (dir, lowercased name); any group with >1 distinct original spelling collides.
const groups = new Map();
for (const f of files) {
  const key = `${f.dir}\u0000${f.name.toLowerCase()}`;
  if (!groups.has(key)) groups.set(key, []);
  groups.get(key).push(f.name);
}

const collisions = [...groups.values()].filter((names) => new Set(names).size > 1);

if (collisions.length > 0) {
  console.error(`ERROR: ${collisions.length} case-collision group(s) found — these coexist on case-sensitive file systems but break Windows builds (#22):`);
  for (const names of collisions) {
    console.error(`  ${names.join('  <->  ')}`);
  }
  console.error('Rename one side so the pair differs by more than case (see issue #22 for the FeesOverview.tsx vs feesOverview.ts precedent).');
  process.exit(1);
}

console.log(`case-collision gate: clean (${files.length} files scanned under ${roots.join(', ')})`);
