"""Usage: python3 -I scrub-binary.py <envelope> [<envelope>...]

Exits 1 if an envelope has no event.minidump attachment item (the Electron
native-crash fixture must contain one).

Minidumps embed stack memory with this machine's environment (paths, user
name, service names). This overwrites every printable ASCII run that leaks
machine data with 'x' of the same length, in the binary payload only (after the
last item header line), so lengths and offsets stay valid. A gzip body is
re-gzipped; the file stays a gzip body.
"""
import gzip
import re
import socket
import sys

me = __import__("getpass").getuser().encode()
host = socket.gethostname().encode()
leak = re.compile(
    rb"/home/|/Users/|/root/|(?<![A-Za-z0-9_])" + re.escape(me) + rb"(?![A-Za-z0-9_])|(?<![A-Za-z0-9_])" + re.escape(host) + rb"(?![A-Za-z0-9_])"
    rb"|[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}"
)
run = re.compile(rb"[\x20-\x7e]{4,}")
ok_email = re.compile(rb"@example\.com$")

for path in sys.argv[1:]:
    raw = open(path, "rb").read()
    gz = raw[:2] == b"\x1f\x8b"
    d = bytearray(gzip.decompress(raw) if gz else raw)
    start = d.find(b'"attachment_type"')
    if start < 0 or b'"attachment_type":"event.minidump"' not in d:
        sys.exit(f"{path}: no event.minidump attachment item")
    start = d.find(b"\n", start) + 1  # payload follows the item header line
    n = 0
    for m in run.finditer(bytes(d[start:])):
        s = m.group(0)
        if leak.search(s) and not ok_email.search(s):
            d[start + m.start() : start + m.end()] = b"x" * len(s)
            n += 1
    out = gzip.compress(bytes(d), mtime=0) if gz else bytes(d)
    open(path, "wb").write(out)
    print(f"{path}: scrubbed {n} runs")
