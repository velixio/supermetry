import { describe, it, expect } from 'vitest';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
const root=dirname(dirname(fileURLToPath(import.meta.url)));
describe('generated token outputs',()=>{it('contain the current semantic values from tokens.json',async()=>{const tokens=JSON.parse(await readFile(join(root,'tokens.json'),'utf8')); const css=await readFile(join(root,'dist/tokens.css'),'utf8'); for (const theme of ['light','dark']) for(const group of Object.values(tokens.semantic[theme]) as any[]) for(const item of Object.values(group) as any[]) { if(item && typeof item === 'object' && '$value' in item) expect(css).toContain(item.$value); } expect(css).toContain('[data-theme=dark], .dark'); expect(css).toContain('[data-density=compact]'); });});
