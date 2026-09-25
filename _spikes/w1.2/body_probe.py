"""W1.2 byte-parity probe: the request bodies typesafe-sdk-python 0.7.1 sends.

The oracle of TestBodyBytesMatchPython and TestBodyDeviationsFromPython in the
root package (and of W2.3's re-assertion through the client): for each case it
builds the request exactly as the SDK does (prepare_system_one) and prints the
case name, a tab and repr() of the body bytes. It needs the upstream project's
own locked environment (pydantic-core 2.46.5), so it runs through uv inside
the upstream checkout at 0ffd094:

  uv run --frozen --project <typesafe-sdk-python> python _spikes/w1.2/body_probe.py

results/body_probe-M.txt is the run the tests cite.
"""

import httpx2

from typesafe_sdk import Noul
from typesafe_sdk._core.config import Config
from typesafe_sdk._core.endpoints import prepare_system_one
from typesafe_sdk._core.response_types import SystemOneResponse

CONFIG = Config("key", "https://api.example", "jev-latest", 10.0, httpx2.Headers())
Q = {"q": {"type": "noul", "instructions": "?"}}
CTL = "".join(chr(c) for c in range(0x20)) + "\x7f<>&\"\\/é  \U0001F600"
FLOATS = [1e-5, 9.99e-6, 1e-4, 0.1, 1.5, 3.0, -7.0, 9999999999999998.0, 1e16, 1e20, 1e21, 1e22,
          5e-324, 1.7976931348623157e308, -0.0, 0.0, float(2**53 + 1), 12345678901234567168.0]

CASES = {
    "string": dict(state="I was charged twice. Please help.", questions=Q),
    "map": dict(state={"message": "I was charged twice."}, questions=Q),
    "nested": dict(
        state={"name": "ticket", "items": [{"id": 1, "text": "hi", "tags": ["a", "b"], "ok": True,
                                            "meta": {"source": "web", "rank": 2, "note": None}}]},
        questions=Q,
    ),
    "array": dict(state=[{"message": "Classify"}, None], questions=Q),
    "rawjson": dict(state={"a": [1, 2, {"b": None}], "c": "d"}, questions=Q),
    "control": dict(state=CTL, questions=Q),
    "floats": dict(state=FLOATS, questions=Q),
    "extra-body": dict(state="hi", questions=Q, model="call-model",
                       extra_body={"model": "override-model", "beam_width": 4, "nullable": None}),
    "extra-state-questions": dict(state="ignored", questions=Q,
                                  extra_body={"questions": {"x": {"type": "noul"}}, "state": {"s": 1}, "z": [1]}),
    "typed-questions": dict(state="x", questions={"billing": Noul(instructions="Is this about billing?")}),
}

for name, kw in CASES.items():
    request = prepare_system_one(CONFIG, kw["state"], kw["questions"], kw.get("model"), kw.get("extra_body"),
                                 None, None, SystemOneResponse)
    print(name, repr(request.content), sep="\t")
