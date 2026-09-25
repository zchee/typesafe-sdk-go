"""Messages the Python SDK 0.7.1 derives from unsuccessful response bodies.

Run from the upstream checkout (typesafe-sdk-python @ 0ffd094) with its own
virtualenv:

    cd typesafe-sdk-python && .venv/bin/python <this file>

Each line is "<hex of the body> <repr of str(error)>" for an APIError with
status 400 and no endpoint, built as the SDK builds one
(api_error(status, deserialize(content), headers)). internal/codec's
TestReadErrorBody and the root's TestAPIErrorBodyEdgeCases pin the same
bodies.
"""

import sys

sys.path.insert(0, ".")

import httpx2  # noqa: E402

from typesafe_sdk._core.errors import api_error  # noqa: E402
from typesafe_sdk._core.json import deserialize  # noqa: E402

BODIES = [
    b"",
    b"null",
    b"[]",
    b"42",
    b"true",
    b"not JSON: \xff",
    b"x" * 201,
    b'{"unknown":"' + b"x" * 201 + b'"}',
    b'{"error":"","message":"ignored"}',
    b'{"detail":[null,42,{"msg":4}]}',
    b'{"error":"error","message":"message","detail":"detail"}',
    b'{"error":{"message":"nested error"},"message":"message"}',
    b'{"message":"message","detail":"detail"}',
    b'{"detail":"detail"}',
    b'{"detail":{"message":"nested detail"}}',
    b'{"detail":[{"loc":["body","questions","q","score","criteria",0],"msg":"Invalid"},{"msg":"Missing"},{}]}',
    b"plain text",
    b'{"unexpected":true}',
    b'""',
    b'"plain"',
    b"   ",
    b'{"message":"a","message":"b"}',
    b'{"detail":[{"loc":["body",null,true,false,-0,7],"msg":"m"}]}',
    b'{"detail":{"message":"denied","error_type":"authentication_error"}}',
    b"{\n  \"a\" : 1 }",
    b'{"a":"\x01"}',
    b"\xe2\x82A",
    b"\xed\xa0\x80",
    b"\xf0\x9f",
    b"\xc0\xaf",
    b"\xff\xfe",
    b"\xf4\x90\x80\x80",
    b"\xe0\x80\x80",
    b"ok\xf0\x9f\x98",
    b'{"error":null,"message":"m"}',
    b'{"detail":[]}',
    b'{"detail":[{"msg":""}]}',
    b'{"message":"x","v":NaN}',
    b'{"message":"a\\ud800"}',
    b"NaN",
    b"Infinity",
]

for content in BODIES:
    error = api_error(400, deserialize(content), httpx2.Headers())
    print(content.hex() or "-", repr(str(error)))
