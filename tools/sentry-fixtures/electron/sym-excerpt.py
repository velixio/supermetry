#!/usr/bin/env python3
"""Cut a small Breakpad .sym excerpt from a public Electron symbol file.

Usage: python3 -I sym-excerpt.py <electron.sym> <hex-rel-addr>... > excerpt.sym

Keeps the MODULE and INFO lines, every FUNC (with its INLINE and line
records) that contains one of the addresses or the address minus one (the
call site of a caller frame), the FILE and INLINE_ORIGIN lines those records
use, and every STACK CFI INIT block (with its deltas) covering them. The
output is a prefix of facts, never edited by hand, so the checked-in excerpt
can be regenerated from the public file. Prints the function name of each
address to stderr.
"""
import bisect
import sys


def main():
    path, addrs = sys.argv[1], sorted({int(a, 16) for a in sys.argv[2:]})
    probes = sorted({a for x in addrs for a in (x, x - 1)})

    def hit(start, size):
        i = bisect.bisect_left(probes, start)
        return i < len(probes) and probes[i] < start + size

    head, files, origins = [], {}, {}
    funcs = []  # (name, [lines])
    cfi = []  # lines
    cur = None  # current kept FUNC block
    cfi_keep = False
    names = {}
    used_files, used_origins = set(), set()

    with open(path, "r", errors="replace") as f:
        for line in f:
            line = line.rstrip("\n")
            tag = line.split(" ", 1)[0]
            if tag in ("MODULE", "INFO"):
                head.append(line)
            elif tag == "FILE":
                p = line.split(" ", 2)
                files[int(p[1])] = line
            elif tag == "INLINE_ORIGIN":
                p = line.split(" ", 2)
                origins[int(p[1])] = line
            elif tag == "FUNC":
                p = line.split(" ")
                if p[1] == "m":
                    p = [p[0]] + p[2:]
                start, size = int(p[1], 16), int(p[2], 16)
                if hit(start, size):
                    cur = []
                    funcs.append((" ".join(p[4:]), cur))
                    cur.append(line)
                    for a in addrs:
                        if start <= a < start + size:
                            names[a] = " ".join(p[4:])
                else:
                    cur = None
            elif tag == "INLINE":
                if cur is not None:
                    cur.append(line)
                    p = line.split(" ")
                    used_files.add(int(p[3]))
                    used_origins.add(int(p[4]))
            elif tag == "PUBLIC":
                cur = None
            elif tag == "STACK":
                cur = None
                p = line.split(" ")
                if p[1] == "CFI" and p[2] == "INIT":
                    start, size = int(p[3], 16), int(p[4], 16)
                    cfi_keep = hit(start, size)
                    if cfi_keep:
                        cfi.append(line)
                elif p[1] == "CFI" and cfi_keep:
                    cfi.append(line)
                else:
                    cfi_keep = False
            elif cur is not None:
                cur.append(line)
                p = line.split(" ")
                if len(p) >= 4:
                    used_files.add(int(p[3]))

    out = list(head)
    out += [files[i] for i in sorted(used_files) if i in files]
    out += [origins[i] for i in sorted(used_origins) if i in origins]
    for _, lines in funcs:
        out += lines
    out += cfi
    sys.stdout.write("\n".join(out) + "\n")
    for a in addrs:
        print("%x %s" % (a, names.get(a, "?")), file=sys.stderr)


main()
