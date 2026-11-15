import traceback

from supermetry_apps.frames import build_frames, frames_from_traceback, scrub_frames, scrub_path


def fs(path, line, name):
    return traceback.FrameSummary(path, line, name)


def test_parses_traceback_frames_and_scrubs_golden():
    summary = [
        fs("/home/alice/proj/app/main.py", 10, "run"),
        fs("/Users/Bob/proj/app/util.py", 22, "helper"),
        fs("/opt/build/a/b/c/lib/mod.py", 7, "<module>"),
        fs("/usr/lib/python3.12/site-packages/requests/api.py", 5, "get"),
        fs("<frozen importlib._bootstrap>", 1, "_find"),
        fs("C:\\Users\\Carol\\app\\win.py", 4, "win"),
    ]
    assert build_frames(frames_from_traceback(summary)) == "\n".join(
        [
            "~/proj/app/main.py:10 run",
            "~/proj/app/util.py:22 helper",
            "c/lib/mod.py:7 <module>",
            "~/app/win.py:4 win",
        ]
    )


def test_real_traceback_has_no_message():
    try:
        raise ValueError("secret /home/bob/diary.txt")
    except ValueError as e:
        out = build_frames(frames_from_traceback(traceback.extract_tb(e.__traceback__)), in_app=lambda p: True)
    assert "secret" not in out and "diary" not in out
    assert "test_frames.py" in out


def test_scrub_golden():
    assert scrub_frames("file:///home/Bob/a/b.py:3 f") == "~/a/b.py:3 f"
    assert scrub_frames("/usr/lib/x/y/z/q.py:3 f") == "y/z/q.py:3 f"
    assert scrub_frames("src/rel.py:3 f") == "src/rel.py:3 f"
    assert scrub_frames("C:\\Users\\Dan\\p\\q.py:9 g") == "~/p/q.py:9 g"
    assert scrub_frames("D:\\build\\agent\\work\\src\\q.py:9 g") == "work/src/q.py:9 g"
    assert scrub_path("/home/x") == "~/"


def test_custom_in_app():
    frames = frames_from_traceback([fs("/x/site-packages/m/i.py", 1, "a"), fs("/x/src/j.py", 2, "b")])
    assert build_frames(frames, in_app=lambda p: "site-packages" in p) == "site-packages/m/i.py:1 a"


def test_caps_at_4kb_on_line_boundary():
    lines = [f"src/f{i}.py:{i} fn{i}" for i in range(400)]
    out = scrub_frames("\n".join(lines), 4096)
    assert len(out.encode()) <= 4096
    assert not out.endswith("\n")
    assert "\n".join(lines).startswith(out)
