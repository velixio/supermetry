import json


class FakeServer:
    """In-process fake of POST /api/public/v1/app-events enforcing the wire contract."""

    def __init__(self):
        self.key = "k_test"
        self.site = "app_1"
        self.requests = []
        self.script = []  # ints, or "network" to raise
        self.seen = set()
        self.accepted = []
        self.force_drop = ""

    def transport(self, url, headers, body):
        try:
            parsed = json.loads(body)
        except ValueError:
            parsed = None
        self.requests.append({"url": url, "headers": headers, "body": body, "parsed": parsed})
        nxt = self.script.pop(0) if self.script else None
        if nxt == "network":
            raise OSError("connection refused")
        if isinstance(nxt, int):
            return nxt, ""
        if len(body.encode()) > 16384:
            return 400, ""
        if not parsed or not isinstance(parsed.get("events"), list) or not 1 <= len(parsed["events"]) <= 20:
            return 400, ""
        if headers.get("X-Supermetry-Key") != self.key or parsed.get("site") != self.site:
            return 401, ""
        dropped = []
        for i, e in enumerate(parsed["events"]):
            if self.force_drop:
                dropped.append({"index": i, "reason": self.force_drop})
            elif e["id"] in self.seen:
                dropped.append({"index": i, "reason": "duplicate"})
            else:
                self.seen.add(e["id"])
                self.accepted.append(e)
        return 202, json.dumps({"accepted": len(parsed["events"]) - len(dropped), "dropped": dropped})
