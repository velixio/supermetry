"""Builds the synthetic Android and Cocoa fixtures (run: python3 -I build.py <testdata dir>).

No Android or iOS toolchain is available here, so these are hand-built from the
SDK sources in knowledge/sentry (read-only) and the research notes. Every
sidecar carries "synthetic": true and cites the source lines each shape came
from. All data is invented: user id fixture-user-1, package com.example.fixture,
no real addresses, hostnames or IPs. Bodies are gzip (mtime 0), as those SDKs send.
"""
import gzip
import json
import os
import sys

out_root = sys.argv[1]
KEY = "0123456789abcdef0123456789abcdef"
RELEASE = "com.example.fixture@1.0.0+1"
JAVA = "sentry-java@4fc0b1e"
COCOA = "sentry-cocoa@fedc5e3"
EVENT_ID = "{:032x}"


def compact(o):
    return json.dumps(o, separators=(",", ":"))


def envelope(header, items):
    parts = [compact(header)]
    for item_header, payload in items:
        body = compact(payload)
        h = dict(item_header)
        h["length"] = len(body.encode())
        parts.append(compact(h))
        parts.append(body)
    return ("\n".join(parts) + "\n").encode()


def write(sdk, case, body, client, sdk_ver, notes):
    d = os.path.join(out_root, sdk)
    os.makedirs(d, exist_ok=True)
    open(os.path.join(d, case + ".envelope"), "wb").write(gzip.compress(body, mtime=0))
    side = {
        "method": "POST",
        "path": "/api/42/envelope/",
        "query": "",
        "headers": {
            "Content-Type": "application/x-sentry-envelope",
            "Content-Encoding": "gzip",
            "X-Sentry-Auth": f"Sentry sentry_version=7,sentry_client={client}/{sdk_ver},sentry_key={KEY}",
            "User-Agent": f"{client}/{sdk_ver}",
        },
        "sdk": sdk,
        "sdk_version": f"{client}@{sdk_ver}",
        "synthetic": True,
        "notes": notes,
    }
    open(os.path.join(d, case + ".json"), "w").write(json.dumps(side, indent=2) + "\n")


def android_header(n):
    return {
        "event_id": EVENT_ID.format(n),
        "sent_at": "2026-11-17T10:00:05.000Z",
        "sdk": {"name": "sentry.java.android", "version": "8.64.0"},
    }


def android_event(n, level="fatal", platform="java"):
    return {
        "event_id": EVENT_ID.format(n),
        "timestamp": "2026-11-17T09:59:58.000Z",
        "platform": platform,
        "level": level,
        "release": RELEASE,
        "dist": "1",
        "environment": "fixture",
        "user": {"id": "fixture-user-1"},
        "sdk": {"name": "sentry.java.android", "version": "8.64.0"},
        "contexts": {
            "app": {"app_identifier": "com.example.fixture", "app_name": "Fixture", "app_version": "1.0.0", "app_build": "1", "in_foreground": True},
            "device": {"family": "Pixel", "model": "FixturePhone", "arch": "arm64-v8a", "simulator": False, "online": True},
            "os": {"name": "Android", "version": "14", "build": "UP1A.000000.000"},
        },
        "tags": {},
    }


JVM_FRAMES = [
    {"filename": "Looper.java", "function": "loopOnce", "module": "android.os.Looper", "lineno": 232, "in_app": False},
    {"filename": "b.java", "function": "run", "module": "a.b.c.d", "lineno": 41, "in_app": True},
    {"filename": "b.java", "function": "a", "module": "a.b.c.e", "lineno": 17, "in_app": True},
]
PROGUARD_UUID = "6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b"
SESSION_ATTRS = {"release": RELEASE, "environment": "fixture", "ip_address": "{{auto}}", "user_agent": "Dalvik/2.1.0 (Linux; U; Android 14; FixturePhone Build/UP1A.000000.000)"}

# ---- Android ---------------------------------------------------------------
ev = android_event(0xA1)
ev["exception"] = {"values": [{
    "type": "IllegalStateException", "value": "fixture jvm crash", "module": "java.lang", "thread_id": 2,
    "mechanism": {"type": "UncaughtExceptionHandler", "handled": False},
    "stacktrace": {"frames": list(reversed(JVM_FRAMES))},
}]}
ev["threads"] = [{"id": 2, "name": "main", "crashed": True, "current": True, "main": True}]
ev["debug_meta"] = {"images": [{"type": "proguard", "uuid": PROGUARD_UUID}]}
write("android", "jvm-crash", envelope(android_header(0xA1), [({"content_type": "application/json", "type": "event"}, ev)]), "sentry.java.android", "8.64.0",
      f"SYNTHETIC. Shape from {JAVA}: exception mechanism UncaughtExceptionHandler handled=false (sentry/src/main/java/io/sentry/UncaughtExceptionHandlerIntegration.java:168-169); "
      "proguard debug_meta image (sentry/src/main/java/io/sentry/protocol/DebugImage.java:52,254-262; DebugMeta.java:77); gzip + X-Sentry-Auth (sentry/src/main/java/io/sentry/transport/HttpConnection.java:127-132, RequestDetailsResolver.java:46-59); "
      "obfuscated frame names a.b.c.d are invented to show why the mapping UUID matters.")

ev = android_event(0xA2)
ev["timestamp"] = "2026-11-17T09:30:00.000Z"  # ANR v2 is sent on the next launch, event time is the ANR time
ev["exception"] = {"values": [{
    "type": "ApplicationNotResponding", "value": "ANR", "module": "io.sentry.android.core", "thread_id": 1,
    "mechanism": {"type": "AppExitInfo", "handled": False},
    "stacktrace": {"frames": [
        {"filename": "Looper.java", "function": "loopOnce", "module": "android.os.Looper", "lineno": 232, "in_app": False},
        {"filename": "b.java", "function": "run", "module": "a.b.c.d", "lineno": 99, "in_app": True},
        {"filename": "Object.java", "function": "wait", "module": "java.lang.Object", "lineno": -2, "in_app": False},
    ][::-1]},
}]}
ev["threads"] = [{"id": 1, "name": "main", "crashed": True, "current": True, "main": True, "state": "Blocked"}]
ev["debug_meta"] = {"images": [{"type": "proguard", "uuid": PROGUARD_UUID}]}
write("android", "anr", envelope(android_header(0xA2), [({"content_type": "application/json", "type": "event"}, ev)]), "sentry.java.android", "8.64.0",
      f"SYNTHETIC. ANR v2 (API 30+), sent after the next app start with the event timestamp from ApplicationExitInfo: {JAVA} sentry-android-core/src/main/java/io/sentry/android/core/AnrV2Integration.java:122-123,150-196 (level fatal, threads from the system dump); "
      "exception ApplicationNotResponding, value ANR, mechanism type AppExitInfo: ApplicationExitInfoEventProcessor.java:905-930; mechanism string anr_foreground/anr_background lives in the session abnormal_mechanism: AnrV2Integration.java:278-279, ApplicationExitInfoEventProcessor.java:810-822. "
      "mechanism.handled=false and the thread state are inference.")

ev = android_event(0xA3, platform="native")
ev["sdk"] = {"name": "sentry.native", "version": "0.17.6"}
ev["exception"] = {"values": [{
    "type": "SIGSEGV", "value": "Segmentation fault", "thread_id": 4242,
    "mechanism": {"type": "signalhandler", "handled": False, "synthetic": True, "meta": {"signal": {"number": 11, "code": 1, "name": "SIGSEGV"}}},
    "stacktrace": {"frames": [
        {"instruction_addr": "0x7a1200abcd", "package": "/data/app/~~fixture/com.example.fixture/lib/arm64/libfixture.so"},
        {"instruction_addr": "0x7a1200abf0", "package": "/data/app/~~fixture/com.example.fixture/lib/arm64/libfixture.so"},
        {"instruction_addr": "0x7a2000aa10", "package": "/apex/com.android.runtime/lib64/bionic/libc.so"},
    ]},
}]}
ev["threads"] = [{"id": 4242, "name": "main", "crashed": True, "current": True}]
ev["debug_meta"] = {"images": [
    {"type": "elf", "debug_id": "7a3c9e10-55b2-4b8e-8a53-2f0c8d4e9a01", "code_id": "109e3c7ab2558e4b8a532f0c8d4e9a01c0ffee00", "code_file": "/data/app/~~fixture/com.example.fixture/lib/arm64/libfixture.so", "image_addr": "0x7a12000000", "image_size": 1048576, "arch": "arm64"},
    {"type": "elf", "debug_id": "11111111-2222-4333-8444-555555555555", "code_id": "aaaaaaaabbbbccccddddeeeeeeeeeeee00000000", "code_file": "/apex/com.android.runtime/lib64/bionic/libc.so", "image_addr": "0x7a20000000", "image_size": 2097152, "arch": "arm64"},
]}
write("android", "ndk-crash", envelope(android_header(0xA3), [({"content_type": "application/json", "type": "event"}, ev)]), "sentry.java.android", "8.64.0",
      f"SYNTHETIC. NDK crash written by the native library into the outbox and sent on next launch: {JAVA} sentry-android-core/src/main/java/io/sentry/android/core/AndroidOptionsInitializer.java:445-468, sentry-android-ndk/src/main/java/io/sentry/android/ndk/SentryNdk.java:48-60. "
      "debug_meta image keys type/debug_id/code_id/code_file/image_addr/image_size/arch: sentry/src/main/java/io/sentry/protocol/DebugImage.java:254-262. sentry-native (v0.17.6, gradle/libs.versions.toml:199) is NOT cloned, so the elf image type, the signalhandler mechanism, "
      "the sdk name sentry.native and raw instruction_addr frames are inference (research 0a-sentry-mobile-and-uploads.md A.1).")

sess = {"sid": "3f1c2a40-8a8e-4f43-9d52-111111111111", "did": "fixture-user-1", "init": False, "started": "2026-11-17T09:00:00.000Z", "status": "exited", "seq": 31, "errors": 0, "duration": 600, "timestamp": "2026-11-17T09:10:00.000Z", "attrs": SESSION_ATTRS}
write("android", "session-update", envelope({"sent_at": "2026-11-17T09:10:01.000Z", "sdk": {"name": "sentry.java.android", "version": "8.64.0"}}, [({"type": "session"}, sess)]), "sentry.java.android", "8.64.0",
      f"SYNTHETIC. Session item with the fields in {JAVA} sentry/src/main/java/io/sentry/Session.java:397-445 (sid, did, init, started, status, seq, errors, duration, timestamp, attrs); attrs release/environment/ip_address/user_agent as in research A.1; "
      "end of a session after 30 s in background: LifecycleWatcher.java:58-129. status exited is the update that closes a session.")

# ---- Cocoa -----------------------------------------------------------------
def cocoa_header(n):
    return {"event_id": EVENT_ID.format(n), "sent_at": "2026-11-17T10:00:05.000Z", "sdk": {"name": "sentry.cocoa", "version": "9.35.0"}}


def cocoa_event(n, level):
    return {
        "event_id": EVENT_ID.format(n),
        "timestamp": "2026-11-17T09:59:58.000Z",
        "platform": "cocoa",
        "level": level,
        "release": "com.example.fixture@1.0.0+1",
        "dist": "1",
        "environment": "fixture",
        "user": {"id": "fixture-user-1"},
        "sdk": {"name": "sentry.cocoa", "version": "9.35.0"},
        "contexts": {
            "app": {"app_identifier": "com.example.fixture", "app_name": "Fixture", "app_version": "1.0.0", "app_build": "1"},
            "device": {"family": "iOS", "model": "iPhone15,2", "arch": "arm64", "simulator": False},
            "os": {"name": "iOS", "version": "18.1", "build": "22B000"},
        },
        "tags": {},
    }


MACHO = [
    {"type": "macho", "debug_id": "84a04d24-0e60-3810-a8c0-90a65e2df61a", "image_addr": "0x0000000102000000", "image_size": 1638400, "image_vmaddr": "0x0000000100000000", "code_file": "/private/var/containers/Bundle/Application/00000000-0000-0000-0000-000000000000/Fixture.app/Fixture"},
    {"type": "macho", "debug_id": "0b9f4c3e-6a1d-3c52-8e07-4d9a2b6c1f30", "image_addr": "0x00000001a0000000", "image_size": 8388608, "image_vmaddr": "0x00000001a0000000", "code_file": "/usr/lib/system/libsystem_kernel.dylib"},
]
FRAMES_APP = [
    {"instruction_addr": "0x00000001a0003b10", "image_addr": "0x00000001a0000000", "symbol_addr": "0x00000001a0003b00", "function": "__pthread_kill", "package": "/usr/lib/system/libsystem_kernel.dylib"},
    {"instruction_addr": "0x0000000102001234", "image_addr": "0x0000000102000000", "symbol_addr": "0x0000000102001200", "function": "<redacted>", "package": "Fixture"},
]

ev = cocoa_event(0xC1, "fatal")
ev["exception"] = {"values": [{
    "type": "EXC_BAD_ACCESS", "value": "Exception 1, Code 1, Subcode 16",
    "mechanism": {"type": "mach", "handled": False, "synthetic": True,
                  "meta": {"mach_exception": {"name": "EXC_BAD_ACCESS", "exception": 1, "code": 1, "subcode": 16},
                           "signal": {"number": 11, "code": 0, "code_name": "SEGV_MAPERR", "name": "SIGSEGV"}},
                  "data": {"relevant_address": "0x0000000000000010"}},
    "thread_id": 0,
    "stacktrace": {"frames": list(reversed(FRAMES_APP))},
}]}
ev["threads"] = [{"id": 0, "name": "main", "crashed": True, "current": True, "stacktrace": {"frames": list(reversed(FRAMES_APP))}}]
ev["debug_meta"] = {"images": MACHO}
write("cocoa", "crash", envelope(cocoa_header(0xC1), [({"content_type": "application/json", "type": "event"}, ev)]), "sentry.cocoa", "9.35.0",
      f"SYNTHETIC. Crash written by the crash handler and sent next launch ({COCOA} Sources/Swift/Integrations/KSCrash/SentryKSCrash+Integration.swift:43). Exception type/value from a mach exception: Sources/Sentry/SentryCrashReportConverter.m:443-451; "
      "mechanism type mach, handled false, synthetic true, meta.mach_exception/signal, data.relevant_address: SentryCrashReportConverter.m:729-766; frame keys instruction_addr/image_addr/symbol_addr/function/package: Sources/Swift/Protocol/Codable/SentryFrameCodable.swift:13-20; "
      "macho debug_meta images (debug_id, image_addr, image_size, image_vmaddr, code_file): Sources/Swift/Protocol/Codable/SentryDebugMetaCodable.swift:19-25, Sources/Swift/SentryCrash/SentryDebugImageProvider.swift:43-80. UUIDs and addresses are invented.")

ev = cocoa_event(0xC2, "error")
ev["exception"] = {"values": [{
    "type": "App Hang Fully Blocked", "value": "App hanging for at least 2000 ms.",
    "mechanism": {"type": "AppHang", "handled": True, "data": {"app_hang_duration": "at least 2000 ms"}},
    "stacktrace": {"frames": list(reversed(FRAMES_APP)), "snapshot": True},
}]}
ev["threads"] = [{"id": 0, "name": "main", "current": True, "crashed": False, "stacktrace": {"frames": list(reversed(FRAMES_APP)), "snapshot": True}}]
ev["debug_meta"] = {"images": MACHO}
write("cocoa", "app-hang", envelope(cocoa_header(0xC2), [({"content_type": "application/json", "type": "event"}, ev)]), "sentry.cocoa", "9.35.0",
      f"SYNTHETIC. {COCOA} Sources/Swift/Integrations/AppHangTracking/SentryHangTrackingIntegration.swift:98-121 (message 'App hanging for at least N ms.', mechanism AppHang, data key app_hang_duration at :24, stacktrace snapshot=true, threads, debug_meta); "
      "exception types: Sources/Swift/Integrations/AppHangTracking/SentryAppHangTypeMapper.swift:8-12. Level error and handled=true for a non-fatal hang are inference (fatal variants set handled=false at SentryHangTrackingIntegration.swift:190).")

ev = cocoa_event(0xC3, "fatal")
ev["exception"] = {"values": [{
    "type": "WatchdogTermination", "value": "The OS watchdog terminated your app, possibly because it overused RAM.",
    "mechanism": {"type": "watchdog_termination", "handled": False},
}]}
ev["breadcrumbs"] = [{"type": "navigation", "category": "app.lifecycle", "level": "info", "timestamp": "2026-11-17T09:59:00.000Z", "data": {"state": "foreground"}}]
write("cocoa", "watchdog-termination", envelope(cocoa_header(0xC3), [({"content_type": "application/json", "type": "event"}, ev)]), "sentry.cocoa", "9.35.0",
      f"SYNTHETIC. {COCOA} Sources/Swift/Integrations/WatchdogTerminations/SentryWatchdogTerminationConstants.swift:5-7 (type, value, mechanism type), SentryWatchdogTerminationTracker.swift:64-83 (level fatal, handled=false, no stack, breadcrumbs, user, dist and environment restored from the previous run). "
      "Reported on the next launch, so the event timestamp is older than sent_at. Breadcrumb shape is inference.")

sess = {"sid": "3f1c2a40-8a8e-4f43-9d52-222222222222", "did": "fixture-user-1", "init": False, "started": "2026-11-17T09:00:00.000Z", "status": "exited", "seq": 1, "errors": 0, "duration": 600.0, "timestamp": "2026-11-17T09:10:00.000Z", "attrs": {"release": RELEASE, "environment": "fixture"}}
write("cocoa", "session-update", envelope({"sent_at": "2026-11-17T09:10:01.000Z", "sdk": {"name": "sentry.cocoa", "version": "9.35.0"}}, [({"type": "session"}, sess)]), "sentry.cocoa", "9.35.0",
      f"SYNTHETIC. Session item fields from {COCOA} Sources/Swift/SentrySession.swift:94-154,228-243 (sid, did, init, started, status, seq, errors, duration, timestamp, attrs{{release,environment}} only). Status exited closes a session after 30 s in background (SessionTracker.swift:173).")
