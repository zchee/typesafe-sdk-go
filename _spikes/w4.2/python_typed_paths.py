"""Field paths the Python SDK 0.7.1 reports for typed response models.

Run from the upstream checkout (typesafe-sdk-python @ 0ffd094) with its own
virtualenv:

    cd typesafe-sdk-python && .venv/bin/python <this file>

Each line is "<form> <case>: <field_path repr>" or "<form> <case>: accepted
<summary>". Two forms of response_model are probed, the two that
tests/test_pydantic_response_models.py uses:

- "nested": a plain BaseModel with the answers under an "answers" model,
  as KnownResponse is (P1, P4 case 1, P5).
- "flat": a SystemOneResponse subclass whose answer fields are lifted to
  the top level, as TypedSystemOneResponse is (P3, P4 case 2).

The Go port's DecodeAs has one form (a flat struct read by wire name); the
table in decodeas.go maps each case to the path Go reports.
"""

import copy
import sys
from typing import Literal

sys.path.insert(0, ".")

import httpx2  # noqa: E402
from pydantic import BaseModel, ConfigDict  # noqa: E402

from typesafe_sdk import (  # noqa: E402
    ChoiceAnswer,
    NoulAnswer,
    ScoreAnswer,
    SystemOneResponse,
    TypeSafeAPIResponseValidationError,
)
from typesafe_sdk._core.schemas.base import parse_response  # noqa: E402

RESULT = {
    "model": "jev-latest",
    "usage": {"input_tokens": 12, "output_tokens": 3},
    "answers": {
        "spam": {"type": "noul", "noul": 0.98},
        "tone": {"type": "choice", "choice": "friendly", "confidence": 0.9, "probabilities": {"friendly": 0.9, "hostile": 0.1}},
        "quality": {
            "type": "score",
            "score": 1.7,
            "confidence": 0.8,
            "legend": {"0": "bad", "1": "ok", "2": "great"},
            "probabilities": {"0": 0.1, "1": 0.1, "2": 0.8},
        },
    },
}


class ToneProbabilities(BaseModel):
    friendly: float
    hostile: float


class Tone(BaseModel):
    model_config = ConfigDict(extra="ignore", frozen=True)

    choice: Literal["friendly", "hostile"]
    probabilities: ToneProbabilities


class Flat(SystemOneResponse):
    spam: NoulAnswer
    tone: Tone
    quality: ScoreAnswer
    missing: NoulAnswer | None = None


class FlatPlain(SystemOneResponse):
    """The flat form with SDK answer types only (no Literal)."""

    spam: NoulAnswer
    tone: ChoiceAnswer
    quality: ScoreAnswer
    missing: NoulAnswer | None = None


class NestedAnswers(BaseModel):
    spam: NoulAnswer
    tone: Tone
    quality: ScoreAnswer
    missing: NoulAnswer | None = None


class Nested(BaseModel):
    model: str
    answers: NestedAnswers


def body(**changes):
    b = copy.deepcopy(RESULT)
    for name, value in changes.items():
        if value is None:
            del b["answers"][name]
        else:
            b["answers"][name] = value
    return b


CASES = {
    "all present": body(),
    "optional present": body(missing={"type": "noul", "noul": 0.25}),
    "required spam absent": body(spam=None),
    "required tone absent": body(tone=None),
    "spam is a choice (wrong kind)": body(spam=RESULT["answers"]["tone"]),
    "tone is a noul (wrong kind)": body(tone={"type": "noul", "noul": 0.5}),
    "quality is a noul (wrong kind)": body(quality={"type": "noul", "noul": 0.5}),
    "optional missing is a choice (wrong kind)": body(missing=RESULT["answers"]["tone"]),
    "tone.choice not an option": body(tone={**RESULT["answers"]["tone"], "choice": "unknown"}),
    "tone probability of an unknown option": body(
        tone={**RESULT["answers"]["tone"], "probabilities": {"friendly": 0.9, "hostile": 0.05, "other": 0.05}}
    ),
    "tone probability of an option missing": body(tone={**RESULT["answers"]["tone"], "probabilities": {"friendly": 1.0}}),
    "quality level 3 of 3 levels": body(
        quality={**RESULT["answers"]["quality"], "probabilities": {"0": 0.1, "1": 0.1, "2": 0.7, "3": 0.1}}
    ),
    "quality legend level 3 of 3 levels": body(
        quality={**RESULT["answers"]["quality"], "legend": {"0": "bad", "1": "ok", "2": "great", "3": "wow"}}
    ),
    "an unknown type named like a field": body(spam={"type": "future", "value": 1}),
    "an unknown type named like the optional field": body(missing={"type": "future", "value": 1}),
    "an extra answer": body(extra={"type": "noul", "noul": 0.5}),
    "an extra member in spam": body(spam={"type": "noul", "noul": 0.98, "explanation": "spammy"}),
    "no answers member": {"model": "jev-latest", "usage": {"input_tokens": 1, "output_tokens": 1}},
}

for form, model in (("flat", Flat), ("flat-plain", FlatPlain), ("nested", Nested)):
    for name, value in CASES.items():
        try:
            result = parse_response(httpx2.Response(200, json=value), model)
            answers = result.answers if form == "nested" else result
            missing = getattr(answers, "missing", "n/a")
            print(f"{form} {name}: accepted missing={missing!r}")
        except TypeSafeAPIResponseValidationError as error:
            print(f"{form} {name}: {error.field_path!r}")
