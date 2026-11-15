"""Frame parsing and scrubbing: `path:line function` lines, first-party only, no absolute paths."""

import re
from typing import Callable, Iterable, List, NamedTuple, Optional

MAX_FRAMES_BYTES = 4096

_HOME_RE = re.compile(r"^(?:/Users/[^/]+|/home/[^/]+|[a-z]:/Users/[^/]+)(?:/|$)", re.IGNORECASE)
_LINE_RE = re.compile(r"^(.*?):(\d+)(.*)$")


class Frame(NamedTuple):
    path: str
    line: int
    fn: str


def default_in_app(path: str) -> bool:
    """Drops dependencies and frozen modules."""
    p = path.replace("\\", "/")
    return not ("node_modules" in p or "site-packages" in p or "dist-packages" in p or p.startswith("<frozen"))


def frames_from_traceback(summaries: Iterable) -> List[Frame]:
    """Accepts traceback.extract_tb() output (FrameSummary objects)."""
    return [Frame(s.filename, s.lineno or 0, s.name) for s in summaries]


def scrub_path(path: str) -> str:
    """Home folders become `~/`; other absolute paths keep their last 3 segments."""
    p = re.sub(r"^file://", "", path, flags=re.IGNORECASE).replace("\\", "/")
    if re.match(r"^/[a-z]:/", p, re.IGNORECASE):
        p = p[1:]
    m = _HOME_RE.match(p)
    if m:
        return "~/" + p[m.end():]
    absolute = p.startswith("/") or re.match(r"^[a-z]:/", p, re.IGNORECASE) is not None
    if not absolute:
        return p
    segs = [s for s in re.sub(r"^[a-z]:", "", p, flags=re.IGNORECASE).split("/") if s]
    return "/".join(segs[-3:])


def scrub_frames(text: str, max_bytes: int = MAX_FRAMES_BYTES) -> str:
    """Scrubs newline-separated `path:line function` lines and caps the result on a line boundary."""
    out: List[str] = []
    size = 0
    for raw in text.split("\n"):
        if not raw.strip():
            continue
        m = _LINE_RE.match(raw)
        line = scrub_path(m.group(1)) + ":" + m.group(2) + m.group(3) if m else scrub_path(raw)
        add = len(line.encode("utf-8")) + (1 if out else 0)
        if size + add > max_bytes:
            break
        out.append(line)
        size += add
    return "\n".join(out)


def build_frames(
    frames: Iterable[Frame],
    in_app: Optional[Callable[[str], bool]] = None,
    max_bytes: int = MAX_FRAMES_BYTES,
) -> str:
    keep = in_app or default_in_app
    lines = [f"{f.path}:{f.line} {f.fn}" for f in frames if keep(re.sub(r"^file://", "", f.path, flags=re.IGNORECASE))]
    return scrub_frames("\n".join(lines), max_bytes)
