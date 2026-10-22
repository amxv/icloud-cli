#!/usr/bin/env node
// Prepare the Build Output API v3 static format. Runs in GitHub Actions after
// Bun/Astro builds docs and before deploying with a project-scoped Vercel token.
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

export function preparePrebuilt(docsRoot = process.cwd()) {
  const dist = path.join(docsRoot, 'dist');
  if (!existsSync(path.join(dist, 'index.html'))) {
    throw new Error('Astro dist/index.html is missing. Run bun run build in GitHub Actions.');
  }
  const output = path.join(docsRoot, '.vercel', 'output');
  rmSync(output, { recursive: true, force: true });
  mkdirSync(output, { recursive: true });
  cpSync(dist, path.join(output, 'static'), { recursive: true });
  // Keep the historical docs redirects when using Vercel's prebuilt output.
  const projectConfig = JSON.parse(readFileSync(path.join(docsRoot, 'vercel.json'), 'utf8'));
  const redirects = (projectConfig.redirects ?? []).map(({ source, destination, permanent }) => ({
    src: source.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'),
    status: permanent ? 308 : 307,
    headers: { Location: destination }
  }));
  const config = {
    version: 3,
    routes: [
      ...redirects,
      { handle: 'filesystem' },
      { src: '/', dest: '/index.html' },
      { src: '/(.*)/', dest: '/$1/index.html' },
      { src: '/(.*)', dest: '/$1/index.html' }
    ]
  };
  writeFileSync(path.join(output, 'config.json'), JSON.stringify(config, null, 2) + '\n');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  preparePrebuilt();
  console.log('Prepared Vercel Build Output API v3 static bundle.');
}
