// Copyright 2026 The typesafe-sdk-go Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sd1

const (
	swarOnes = 0x0101010101010101
	swarHigh = 0x8080808080808080
)

// HasControlByte reports whether s holds any byte below 0x20, eight bytes at a
// time. It is the fast half of the unconditional whole-body rule measured
// against the lazy rule of [visitor.checkString]: a body without such a byte
// has no raw control character inside a string, and JSON whitespace outside
// strings (TAB, LF, CR) is the only reason the slow half runs.
func HasControlByte(s string) bool {
	i := 0
	for ; i+8 <= len(s); i += 8 {
		b := s[i : i+8]
		// The compiler merges these byte loads into one 64-bit load.
		x := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
			uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
		if (x-0x20*swarOnes)&^x&swarHigh != 0 {
			return true
		}
	}
	for ; i < len(s); i++ {
		if s[i] < 0x20 {
			return true
		}
	}
	return false
}

// controlInString reports whether a raw byte below 0x20 sits inside a string
// of body, which sonic has already parsed: escapes are well formed, so a
// backslash inside a string consumes the next byte, and quotes toggle the
// state.
func controlInString(body string) bool {
	in := false
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '"':
			in = !in
		case !in:
		case c == '\\':
			i++
		case c < 0x20:
			return true
		}
	}
	return false
}

// RawControlInString is the unconditional whole-body rule: the SWAR scan,
// then the string-tracking scan only when the body holds a byte below 0x20.
func RawControlInString(body string) bool {
	return HasControlByte(body) && controlInString(body)
}
