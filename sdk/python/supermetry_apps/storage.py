import json
import os
from typing import List


class JsonFileStorage:
    """Optional persistence adapter: keeps the unsent queue in a JSON file across restarts."""

    def __init__(self, path: str) -> None:
        self.path = path

    def load(self) -> List[dict]:
        try:
            with open(self.path, "r", encoding="utf-8") as f:
                data = json.load(f)
            return data if isinstance(data, list) else []
        except (OSError, ValueError):
            return []

    def save(self, events: List[dict]) -> None:
        tmp = self.path + ".tmp"
        try:
            with open(tmp, "w", encoding="utf-8") as f:
                json.dump(events, f)
            os.replace(tmp, self.path)
        except OSError:
            pass
