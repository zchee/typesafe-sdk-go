"""What the Python SDK 0.7.1 writes for its responses and answers.

Run from the upstream checkout (typesafe-sdk-python @ 0ffd094) with its own
virtualenv, passing the Go module's testdata directory:

    cd typesafe-sdk-python && .venv/bin/python <this file> <go-module>/testdata

For every fixture the Python SDK accepts, one line
"<file>: <model_dump_json()>", or "<file>: len=<n> sha256=<hex>" for the
structured-legend floods (their output is 57 KB and 616 KB), then whether the
output equals the fixture's bytes. A fixture Python refuses prints
"<file>: refused at <field_path>". The answer shapes of
test_answer_attributes_and_dictionary_types (R12) and of the parameters of
test_answer_fields_are_frozen (R14) follow, with Usage and ModelMetadata
values, then model_dump() key sets (R5, R15), then the bodies of the two
classes where Go's payload differs from Python's beyond an escape (review
W2.4 MINOR 2): -0.0 in every known float member, and a member name repeated
inside a structured legend level.
"""

import hashlib
import sys
from pathlib import Path

sys.path.insert(0, ".")

import httpx2  # noqa: E402

from typesafe_sdk import (  # noqa: E402
    ChoiceAnswer,
    ListModelsResponse,
    ModelMetadata,
    NoulAnswer,
    ScoreAnswer,
    SystemOneResponse,
    TypeSafeAPIResponseValidationError,
    Usage,
)

LARGE = 4096


def fixtures(testdata: Path) -> None:
    for path in sorted(testdata.glob("*.json")):
        if path.name.startswith("malformed-"):
            continue
        body = path.read_bytes()
        cls = ListModelsResponse if path.name == "models.json" else SystemOneResponse
        try:
            response = cls.from_http_response(httpx2.Response(200, content=body))
        except TypeSafeAPIResponseValidationError as error:
            print(f"{path.name}: refused at {error.field_path!r}")
            continue
        dumped = response.model_dump_json().encode()
        if len(dumped) > LARGE:
            shown = f"len={len(dumped)} sha256={hashlib.sha256(dumped).hexdigest()}"
        else:
            shown = dumped.decode()
        print(f"{path.name}: {shown}")
        print(f"{path.name}: equals the fixture: {dumped == body}")


def answers() -> None:
    cases = {
        "R12 noul": NoulAnswer(noul=0.98),
        "R12 choice": ChoiceAnswer(choice="billing", confidence=0.9, probabilities={"billing": 0.9, "support": 0.1}),
        "R14 noul": NoulAnswer(noul=0.98),
        "R14 choice": ChoiceAnswer(choice="billing", confidence=1.0, probabilities={"billing": 1.0}),
        "R14 score": ScoreAnswer(score=0.0, confidence=1.0, legend={0: "bad"}, probabilities={0: 1.0}),
        "R11 structured score": ScoreAnswer(
            score=0.0, confidence=1.0, legend={0: {"examples": ["a", {"note": None}]}}, probabilities={0: 1.0}
        ),
        "usage": Usage(input_tokens=12, output_tokens=3),
        "empty usage": Usage(),
        "model card": ModelMetadata(name="jev-latest", description="Fast model", release_date="2026-08-01"),
        "empty response": SystemOneResponse(model="test", usage=Usage()),
        "empty models": ListModelsResponse(models=()),
    }
    for name, value in cases.items():
        print(f"{name}: {value.model_dump_json()}")


# Byte strings, not dicts: a dict cannot hold a repeated key, and these
# are the exact bodies TestResponseJSONDeviations in response_json_test.go
# reads.
DEVIATION_BODIES = {
    "negative zero": (
        b'{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{'
        b'"n":{"type":"noul","noul":-0.0},'
        b'"c":{"type":"choice","choice":"a","confidence":-0.0,"probabilities":{"a":-0.0}},'
        b'"s":{"type":"score","score":-0.0,"confidence":-0.0,"legend":{"0":"x"},"probabilities":{"0":-0.0}}}}'
    ),
    "repeated member in a structured level": (
        b'{"model":"m","usage":{"input_tokens":1,"output_tokens":1},"answers":{'
        b'"s":{"type":"score","score":0.5,"confidence":1,"legend":{"0":{"a":1,"b":2,"a":3}},"probabilities":{"0":1}}}}'
    ),
}


def deviations() -> None:
    for name, body in DEVIATION_BODIES.items():
        response = SystemOneResponse.from_http_response(httpx2.Response(200, content=body))
        print(f"{name}: {response.model_dump_json()}")


def keys() -> None:
    body = SystemOneResponse.from_http_response(
        httpx2.Response(200, json={"model": "m", "usage": {"input_tokens": 1, "output_tokens": 1}, "answers": {}})
    )
    for group in ("nouls", "choices", "scores"):
        getattr(body, group)
    print(f"model_dump() keys after reading nouls/choices/scores: {sorted(body.model_dump())}")


if __name__ == "__main__":
    fixtures(Path(sys.argv[1]))
    answers()
    keys()
    deviations()
