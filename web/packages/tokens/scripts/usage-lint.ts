import { readdir, readFile } from 'node:fs/promises'; import { join, relative } from 'node:path';
const base=join(import.meta.dir,'../../../src'); const pattern=/#(?:[\da-f]{3,8})\b|\boklch\(/gi; let count=0;
async function walk(dir:string):Promise<void>{for(const e of await readdir(dir,{withFileTypes:true})){const p=join(dir,e.name);if(e.isDirectory())await walk(p);else if(/\.(tsx?|css)$/.test(e.name)){const s=await readFile(p,'utf8');for(const m of s.matchAll(pattern)){console.log(`${relative(base,p)}:${s.slice(0,m.index).split('\n').length}: raw colour ${m[0]}`);count++;}}}}
await walk(base); console.log(`Found ${count} raw colour occurrence(s).`);
