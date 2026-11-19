import logging
import os

import uvicorn
from fastapi import FastAPI

app = FastAPI()


@app.get("/health")
def health():
    return {"ok": True}


@app.get("/boom")
def boom():
    raise ValueError("bad id 7")


@app.get("/handled")
def handled():
    # An exception the app catches and logs itself.
    try:
        raise ValueError("bad id 7")
    except ValueError:
        logging.getLogger("app").exception("could not load item")
    return {"ok": False}


if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=int(os.environ.get("PORT", "8000")))
