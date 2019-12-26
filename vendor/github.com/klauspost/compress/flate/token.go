// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flate

const (
	// 2 bits:   type   0 = literal  1=EOF  2=Match   3=Unused
	// 8 bits:   xlength = length - MIN_MATCH_LENGTH
	// 22 bits   xoffset = offset - MIN_OFFSET_SIZE, or literal
	lengthShift = 22
	offsetMask  = 1<<lengthShift - 1
	typeMask    = 3 << 30
	literalType = 0 << 30
	matchType   = 1 << 30
)

// The length code for length X (MIN_MATCH_LENGTH <= X <= MAX_MATCH_LENGTH)
// is lengthCodes[length - MIN_MATCH_LENGTH]
var lengthCodes = [256]uint8{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 8,
	9, 9, 10, 10, 11, 11, 12, 12, 12, 12,
	13, 13, 13, 13, 14, 14, 14, 14, 15, 15,
	15, 15, 16, 16, 16, 16, 16, 16, 16, 16,
	17, 17, 17, 17, 17, 17, 17, 17, 18, 18,
	18, 18, 18, 18, 18, 18, 19, 19, 19, 19,
	19, 19, 19, 19, 20, 20, 20, 20, 20, 20,
	20, 20, 20, 20, 20, 20, 20, 20, 20, 20,
	21, 21, 21, 21, 21, 21, 21, 21, 21, 21,
	21, 21, 21, 21, 21, 21, 22, 22, 22, 22,
	22, 22, 22, 22, 22, 22, 22, 22, 22, 22,
	22, 22, 23, 23, 23, 23, 23, 23, 23, 23,
	23, 23, 23, 23, 23, 23, 23, 23, 24, 24,
	24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
	24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
	24, 24, 24, 24, 24, 24, 24, 24, 24, 24,
	25, 25, 25, 25, 25, 25, 25, 25, 25, 25,
	25, 25, 25, 25, 25, 25, 25, 25, 25, 25,
	25, 25, 25, 25, 25, 25, 25, 25, 25, 25,
	25, 25, 26, 26, 26, 26, 26, 26, 26, 26,
	26, 26, 26, 26, 26, 26, 26, 26, 26, 26,
	26, 26, 26, 26, 26, 26, 26, 26, 26, 26,
	26, 26, 26, 26, 27, 27, 27, 27, 27, 27,
	27, 27, 27, 27, 27, 27, 27, 27, 27, 27,
	27, 27, 27, 27, 27, 27, 27, 27, 27, 27,
	27, 27, 27, 27, 27, 28,
}

var offsetCodes = [256]uint32{
	0, 1, 2, 3, 4, 4, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7,
	8, 8, 8, 8, 8, 8, 8, 8, 9, 9, 9, 9, 9, 9, 9, 9,
	10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10,
	11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11, 11,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12, 12,
	13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13,
	13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13,
	14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14,
	14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14,
	14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14,
	14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14, 14,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
	15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15, 15,
}

type token uint32

type tokens struct {
	tokens [maxStoreBlockSize + 1]token
	n      uint16 // Must be able to contain maxStoreBlockSize
}

// Convert a literal into a literal token.
func literalToken(literal uint32) token { return token(literalType + literal) }

// Convert a < xlength, xoffset > pair into a match token.
func matchToken(xlength uint32, xoffset uint32) token {
	return token(matchType + xlength<<lengthShift + xoffset)
}

// Returns the type of a token
func (t token) typ() uint32 { return uint32(t) & typeMask }

// Returns the literal of a literal token
func (t token) literal() uint8 { return uint8(t) }

// Returns the extra offset of a match token
func (t token) offset() uint32 { return uint32(t) & offsetMask }

func (t token) length() uint8 { return uint8(t >> lengthShift) }

// The code is never more than 8 bits, but is returned as uint32 for convenience.
func lengthCode(len uint8) uint32 { return uint32(lengthCodes[len]) }

// Returns the offset code corresponding to a specific offset
func offsetCode(off uint32) uint32 {
	if off < uint32(len(offsetCodes)) {
		return offsetCodes[off&255]
	} else if off>>7 < uint32(len(offsetCodes)) {
		return offsetCodes[(off>>7)&255] + 14
	} else {
		return offsetCodes[(off>>14)&255] + 28
	}
}
