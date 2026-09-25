"""Field paths the Python SDK 0.7.1 reports for malformed response bodies.

Run from the upstream checkout (typesafe-sdk-python @ 0ffd094) with its own
virtualenv:

    cd typesafe-sdk-python && .venv/bin/python <this file>

Each line is "<case>: <field_path repr>" or "<case>: accepted". The cases are
the rows of internal/codec's TestDecodeFieldPaths and TestModelsFieldPaths
that are not deviations; the deviation rows are probed at the end so the
difference is on record.
"""

import sys

sys.path.insert(0, ".")

import httpx2  # noqa: E402

from typesafe_sdk import (  # noqa: E402
    ListModelsResponse,
    SystemOneResponse,
    TypeSafeAPIResponseValidationError,
)


def r1(answers, model=True):
    body = {"usage": {"input_tokens": 1, "output_tokens": 1}, "answers": answers}
    if model:
        body["model"] = "test"
    return body


def body(answers):
    return {"model": "m", "usage": {"input_tokens": 1, "output_tokens": 1}, "answers": answers}


SYSTEM_ONE = {
    "R1 no model": r1({}, model=False),
    "R1 noul missing": r1({"n": {"type": "noul"}}),
    "R1 noul a string": r1({"n": {"type": "noul", "noul": "0.5"}}),
    "R1 choice without confidence": r1({"c": {"type": "choice", "choice": "a", "probabilities": {}}}),
    "R1 choice without choice": r1({"c": {"type": "choice", "confidence": 0.5, "probabilities": {}}}),
    "R1 legend an array": r1({"s": {"type": "score", "score": 1.0, "confidence": 1.0, "legend": [], "probabilities": {}}}),
    "R1 legend key not a level": r1({"s": {"type": "score", "score": 1.0, "confidence": 1.0, "legend": {"x": "bad"}, "probabilities": {}}}),
    "R1 answer not a mapping": r1({"c": "not-a-mapping"}),
    "score wrong, confidence missing": body({"s": {"type": "score", "score": "x", "legend": {}, "probabilities": {}}}),
    "confidence wrong, score missing": body({"s": {"type": "score", "confidence": "x", "legend": {}, "probabilities": {}}}),
    "legend missing, probabilities wrong": body({"s": {"type": "score", "score": 1, "confidence": 1, "probabilities": []}}),
    "probabilities wrong first on the wire": body({"s": {"probabilities": [], "type": "score", "score": 1, "confidence": 1}}),
    "score probability not a number": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {}, "probabilities": {"0": "x"}}}),
    "score probability key not a level": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {}, "probabilities": {"x": 1}}}),
    "choice probabilities wrong, choice missing": body({"c": {"type": "choice", "confidence": 1, "probabilities": []}}),
    "choice probability a string": body({"c": {"type": "choice", "choice": "a", "confidence": 1, "probabilities": {"a": "x"}}}),
    "choice probability a boolean": body({"c": {"type": "choice", "choice": "a", "confidence": 1, "probabilities": {"a": True}}}),
    "noul a boolean": body({"n": {"type": "noul", "noul": True}}),
    "two bad answers, the first in wire order": body({"a": {"type": "noul"}, "b": {"type": "choice"}}),
    "type not a string": body({"a": {"type": 5}}),
    "type null": body({"a": {"type": None}}),
    "answer type pre-pass before a missing model": {"usage": {}, "answers": {"c": "x"}},
    "missing model before an answer's member": {"usage": {}, "answers": {"n": {"type": "noul"}}},
    "answer type pre-pass before an earlier answer": body({"n": {"type": "noul"}, "c": "x"}),
    "answer type pre-pass on a later integer type": body({"n": {"type": "noul"}, "c": {"type": 1}}),
    "model before usage": {"model": 1, "usage": 1, "answers": {}},
    "usage before answers": {"model": "m", "usage": 1, "answers": []},
    "answers null": {"model": "m", "usage": {}, "answers": None},
    "usage null": {"model": "m", "usage": None},
    "a token count null is absent": {"model": "m", "usage": {"input_tokens": None}},
    "a token count 1.0": {"model": "m", "usage": {"input_tokens": 1.0}},
    "a token count 1.5": {"model": "m", "usage": {"input_tokens": 1.5}},
    "output_tokens a string": {"model": "m", "usage": {"output_tokens": "3"}},
    "model null": {"model": None, "usage": {}},
    "an unknown type before a missing model": {"usage": {}, "answers": {"u": {"type": "aurora"}}},
    "answer type pre-pass past an unknown type": body({"u": {"type": "aurora"}, "c": 1}),
    "an empty legend and probabilities": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {}, "probabilities": {}}}),
    "an integer noul": body({"n": {"type": "noul", "noul": 1}}),
    "legend members of other kinds are not needed": body({"n": {"type": "noul", "noul": 1, "choice": 5, "legend": []}}),
    "deviation: a negative token count": {"model": "m", "usage": {"input_tokens": -1}},
    "deviation: a token count past 2^64-1": {"model": "m", "usage": {"input_tokens": 2**64}},
    "deviation: a level value that is a number": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {"0": 5}, "probabilities": {}}}),
    "deviation: level key -1": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {"-1": "a"}, "probabilities": {}}}),
    "deviation: level key ' 1'": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {" 1": "a"}, "probabilities": {}}}),
    "deviation: level key 1_0": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {"1_0": "a"}, "probabilities": {}}}),
    "deviation: level key 1.0": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {"1.0": "a"}, "probabilities": {}}}),
    "deviation: level key 4294967296": body({"s": {"type": "score", "score": 1, "confidence": 1, "legend": {"4294967296": "a"}, "probabilities": {}}}),
}

CARD = {"name": "test", "description": "Test model", "release_date": "2026-09-14"}
MODELS = {
    "R2 models[1] without name": {"models": [CARD, {"description": "Test model", "release_date": "2026-09-14"}]},
    "R2 models[1] without description": {"models": [CARD, {"name": "test", "release_date": "2026-09-14"}]},
    "R2 models[1] without release_date": {"models": [CARD, {"name": "test", "description": "Test model"}]},
    "models missing": {},
    "models an object": {"models": {}},
    "models null": {"models": None},
    "a card that is not an object": {"models": [1]},
    "two members missing, schema order": {"models": [CARD, {"release_date": "c"}]},
    "a name that is a number": {"models": [{"name": 1, "description": "b", "release_date": "c"}]},
    "the first failing card first": {"models": [{"name": "a", "release_date": "c"}, 5]},
    "a card after a scalar card": {"models": [5, {"name": "a"}]},
    "the second card is null": {"models": [CARD, None]},
    "an unknown top-level member": {"models": [], "x": 1},
}

for label, response_type, cases in (("systemone", SystemOneResponse, SYSTEM_ONE), ("models", ListModelsResponse, MODELS)):
    for name, value in cases.items():
        try:
            response_type.from_http_response(httpx2.Response(200, json=value))
            print(f"{label} {name}: accepted")
        except TypeSafeAPIResponseValidationError as error:
            print(f"{label} {name}: {error.field_path!r}")
