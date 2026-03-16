package main

import (
	"fmt"
	"os"
)

// ── QR Code Generator ──────────────────────────────────────────────
// Pure Go QR code encoder for terminal display.
// Supports byte-mode encoding, versions 2-4, error correction level L.

// GF(256) arithmetic for Reed-Solomon encoding
// Primitive polynomial: x^8 + x^4 + x^3 + x^2 + 1 (0x11d)

var gfExp [512]byte
var gfLog [256]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x >= 256 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsEncode computes Reed-Solomon error correction codewords.
func rsEncode(data []byte, nsym int) []byte {
	// Build generator polynomial
	gen := make([]byte, nsym+1)
	gen[0] = 1
	for i := 0; i < nsym; i++ {
		for j := nsym; j > 0; j-- {
			gen[j] = gfMul(gen[j], gfExp[i]) ^ gen[j-1]
		}
		gen[0] = gfMul(gen[0], gfExp[i])
	}

	// Polynomial division
	result := make([]byte, nsym)
	for _, d := range data {
		coef := d ^ result[0]
		copy(result, result[1:])
		result[nsym-1] = 0
		for j := 0; j < nsym; j++ {
			result[j] ^= gfMul(gen[nsym-1-j], coef)
		}
	}
	return result
}

// QR version parameters (error correction level L)
type qrVersion struct {
	version    int
	size       int // modules per side
	dataBytes  int // total data capacity in bytes
	ecBytes    int // error correction bytes
	alignPos   []int
	numBlocks  int
}

var versions = []qrVersion{
	{2, 25, 34, 10, []int{6, 18}, 1},
	{3, 29, 55, 15, []int{6, 22}, 1},
	{4, 33, 80, 20, []int{6, 26}, 1},
}

// Format info lookup table (mask pattern -> 15-bit format string)
// Error correction level L (01), mask patterns 0-7
var formatInfo = [8]uint16{
	0x77C4, 0x72F3, 0x7DAA, 0x789D,
	0x662F, 0x6318, 0x6C41, 0x6976,
}

// encodeData encodes a string as QR byte-mode data.
func encodeData(text string, ver qrVersion) []byte {
	capacity := ver.dataBytes - ver.ecBytes

	// Bit stream: mode (0100=byte), count (8 bits for V1-9), data, terminator
	bits := make([]bool, 0, capacity*8)
	appendBits := func(val int, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (val>>i)&1 == 1)
		}
	}

	appendBits(0x4, 4) // byte mode
	appendBits(len(text), 8)
	for i := 0; i < len(text); i++ {
		appendBits(int(text[i]), 8)
	}
	appendBits(0, 4) // terminator

	// Pad to byte boundary
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}

	// Convert to bytes
	data := make([]byte, 0, capacity)
	for i := 0; i+7 < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8; j++ {
			if bits[i+j] {
				b |= 1 << (7 - j)
			}
		}
		data = append(data, b)
	}

	// Pad with alternating bytes
	padBytes := []byte{0xEC, 0x11}
	for len(data) < capacity {
		data = append(data, padBytes[(len(data)-len(text)-3)%2])
	}

	// Append error correction
	ec := rsEncode(data, ver.ecBytes)
	return append(data, ec...)
}

// ── Matrix construction ─────────────────────────────────────────────

type qrMatrix struct {
	size    int
	modules [][]bool // true = dark
	used    [][]bool // true = reserved/placed
}

func newMatrix(size int) *qrMatrix {
	m := &qrMatrix{size: size}
	m.modules = make([][]bool, size)
	m.used = make([][]bool, size)
	for i := range m.modules {
		m.modules[i] = make([]bool, size)
		m.used[i] = make([]bool, size)
	}
	return m
}

func (m *qrMatrix) set(row, col int, dark bool) {
	if row >= 0 && row < m.size && col >= 0 && col < m.size {
		m.modules[row][col] = dark
		m.used[row][col] = true
	}
}

func (m *qrMatrix) placeFinder(row, col int) {
	for r := -1; r <= 7; r++ {
		for c := -1; c <= 7; c++ {
			rr, cc := row+r, col+c
			if rr < 0 || rr >= m.size || cc < 0 || cc >= m.size {
				continue
			}
			dark := false
			if r >= 0 && r <= 6 && c >= 0 && c <= 6 {
				if r == 0 || r == 6 || c == 0 || c == 6 {
					dark = true
				} else if r >= 2 && r <= 4 && c >= 2 && c <= 4 {
					dark = true
				}
			}
			m.set(rr, cc, dark)
		}
	}
}

func (m *qrMatrix) placeAlignment(row, col int) {
	for r := -2; r <= 2; r++ {
		for c := -2; c <= 2; c++ {
			rr, cc := row+r, col+c
			if rr < 0 || rr >= m.size || cc < 0 || cc >= m.size || m.used[rr][cc] {
				continue
			}
			dark := r == -2 || r == 2 || c == -2 || c == 2 || (r == 0 && c == 0)
			m.set(rr, cc, dark)
		}
	}
}

func (m *qrMatrix) placeTiming() {
	for i := 8; i < m.size-8; i++ {
		dark := i%2 == 0
		if !m.used[6][i] {
			m.set(6, i, dark)
		}
		if !m.used[i][6] {
			m.set(i, 6, dark)
		}
	}
}

func (m *qrMatrix) reserveFormat() {
	// Around top-left finder
	for i := 0; i <= 8; i++ {
		if i < m.size && !m.used[8][i] {
			m.used[8][i] = true
		}
		if i < m.size && !m.used[i][8] {
			m.used[i][8] = true
		}
	}
	// Around top-right finder
	for i := m.size - 8; i < m.size; i++ {
		if !m.used[8][i] {
			m.used[8][i] = true
		}
	}
	// Around bottom-left finder
	for i := m.size - 7; i < m.size; i++ {
		if !m.used[i][8] {
			m.used[i][8] = true
		}
	}
	// Dark module
	m.set(m.size-8, 8, true)
}

func (m *qrMatrix) placeData(data []byte) {
	bitIdx := 0
	totalBits := len(data) * 8

	col := m.size - 1
	up := true

	for col > 0 {
		if col == 6 {
			col-- // skip timing column
		}
		for row := 0; row < m.size; row++ {
			r := row
			if up {
				r = m.size - 1 - row
			}
			for c := 0; c < 2; c++ {
				cc := col - c
				if cc < 0 || m.used[r][cc] {
					continue
				}
				if bitIdx < totalBits {
					byteIdx := bitIdx / 8
					bitOff := 7 - (bitIdx % 8)
					m.modules[r][cc] = (data[byteIdx]>>bitOff)&1 == 1
				}
				m.used[r][cc] = true
				bitIdx++
			}
		}
		col -= 2
		up = !up
	}
}

func (m *qrMatrix) writeFormat(mask int) {
	bits := formatInfo[mask]

	// Horizontal: left of top-left finder + right of top-right finder
	hPositions := []int{0, 1, 2, 3, 4, 5, 7, 8, m.size - 8, m.size - 7, m.size - 6, m.size - 5, m.size - 4, m.size - 3, m.size - 2}
	for i, col := range hPositions {
		dark := (bits>>(14-i))&1 == 1
		m.modules[8][col] = dark
	}

	// Vertical: below top-left finder + above bottom-left finder
	vPositions := []int{0, 1, 2, 3, 4, 5, 7, 8}
	for i, row := range vPositions {
		dark := (bits>>(14-i))&1 == 1
		m.modules[m.size-1-row][8] = dark
	}
	// Upper part
	vUpper := []int{8, 7, 5, 4, 3, 2, 1, 0} // rows from bottom to top near finder
	for i, row := range vUpper {
		dark := (bits>>(6-i))&1 == 1
		if i <= 6 {
			m.modules[row][8] = dark
		}
	}
}

// ── Masking ─────────────────────────────────────────────────────────

type maskFunc func(row, col int) bool

var maskFuncs = [8]maskFunc{
	func(r, c int) bool { return (r+c)%2 == 0 },
	func(r, c int) bool { return r%2 == 0 },
	func(r, c int) bool { return c%3 == 0 },
	func(r, c int) bool { return (r+c)%3 == 0 },
	func(r, c int) bool { return (r/2+c/3)%2 == 0 },
	func(r, c int) bool { return (r*c)%2+(r*c)%3 == 0 },
	func(r, c int) bool { return ((r*c)%2+(r*c)%3)%2 == 0 },
	func(r, c int) bool { return ((r+c)%2+(r*c)%3)%2 == 0 },
}

func (m *qrMatrix) applyMask(mask int, reserved [][]bool) {
	fn := maskFuncs[mask]
	for r := 0; r < m.size; r++ {
		for c := 0; c < m.size; c++ {
			if !reserved[r][c] && fn(r, c) {
				m.modules[r][c] = !m.modules[r][c]
			}
		}
	}
}

func penaltyScore(m *qrMatrix) int {
	score := 0
	size := m.size

	// Rule 1: consecutive same-color modules in rows and columns
	for r := 0; r < size; r++ {
		count := 1
		for c := 1; c < size; c++ {
			if m.modules[r][c] == m.modules[r][c-1] {
				count++
			} else {
				if count >= 5 {
					score += count - 2
				}
				count = 1
			}
		}
		if count >= 5 {
			score += count - 2
		}
	}
	for c := 0; c < size; c++ {
		count := 1
		for r := 1; r < size; r++ {
			if m.modules[r][c] == m.modules[r-1][c] {
				count++
			} else {
				if count >= 5 {
					score += count - 2
				}
				count = 1
			}
		}
		if count >= 5 {
			score += count - 2
		}
	}

	// Rule 2: 2x2 blocks of same color
	for r := 0; r < size-1; r++ {
		for c := 0; c < size-1; c++ {
			v := m.modules[r][c]
			if v == m.modules[r][c+1] && v == m.modules[r+1][c] && v == m.modules[r+1][c+1] {
				score += 3
			}
		}
	}

	// Rule 3: finder-like patterns
	patterns := [2][11]bool{
		{true, false, true, true, true, false, true, false, false, false, false},
		{false, false, false, false, true, false, true, true, true, false, true},
	}
	for r := 0; r < size; r++ {
		for c := 0; c <= size-11; c++ {
			for _, p := range patterns {
				match := true
				for i := 0; i < 11; i++ {
					if m.modules[r][c+i] != p[i] {
						match = false
						break
					}
				}
				if match {
					score += 40
				}
			}
		}
	}
	for c := 0; c < size; c++ {
		for r := 0; r <= size-11; r++ {
			for _, p := range patterns {
				match := true
				for i := 0; i < 11; i++ {
					if m.modules[r+i][c] != p[i] {
						match = false
						break
					}
				}
				if match {
					score += 40
				}
			}
		}
	}

	// Rule 4: proportion of dark modules
	dark := 0
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			if m.modules[r][c] {
				dark++
			}
		}
	}
	total := size * size
	pct := dark * 100 / total
	prev5 := pct - pct%5
	next5 := prev5 + 5
	d1 := prev5 - 50
	if d1 < 0 {
		d1 = -d1
	}
	d2 := next5 - 50
	if d2 < 0 {
		d2 = -d2
	}
	d1 /= 5
	d2 /= 5
	if d1 < d2 {
		score += d1 * 10
	} else {
		score += d2 * 10
	}

	return score
}

// ── Public API ──────────────────────────────────────────────────────

func generateQR(text string) [][]bool {
	// Pick smallest version that fits
	var ver qrVersion
	found := false
	for _, v := range versions {
		capacity := v.dataBytes - v.ecBytes
		// byte mode overhead: 4 (mode) + 8 (count) + len*8 + 4 (terminator) bits
		needed := (4 + 8 + len(text)*8 + 4 + 7) / 8
		if needed <= capacity {
			ver = v
			found = true
			break
		}
	}
	if !found {
		return nil // too long
	}

	data := encodeData(text, ver)

	// Build matrix with fixed patterns
	m := newMatrix(ver.size)

	// Finder patterns at three corners
	m.placeFinder(0, 0)
	m.placeFinder(0, ver.size-7)
	m.placeFinder(ver.size-7, 0)

	// Alignment patterns
	if len(ver.alignPos) >= 2 {
		for _, r := range ver.alignPos {
			for _, c := range ver.alignPos {
				// Skip if overlapping finder patterns
				if (r < 9 && c < 9) || (r < 9 && c > ver.size-9) || (r > ver.size-9 && c < 9) {
					continue
				}
				m.placeAlignment(r, c)
			}
		}
	}

	// Timing patterns
	m.placeTiming()

	// Reserve format info areas
	m.reserveFormat()

	// Save reserved map before data placement
	reserved := make([][]bool, ver.size)
	for i := range reserved {
		reserved[i] = make([]bool, ver.size)
		copy(reserved[i], m.used[i])
	}

	// Place data
	m.placeData(data)

	// Try all 8 masks, pick lowest penalty
	bestMask := 0
	bestPenalty := -1

	for mask := 0; mask < 8; mask++ {
		// Clone matrix
		trial := newMatrix(ver.size)
		for r := 0; r < ver.size; r++ {
			copy(trial.modules[r], m.modules[r])
			copy(trial.used[r], m.used[r])
		}
		trial.applyMask(mask, reserved)
		trial.writeFormat(mask)

		p := penaltyScore(trial)
		if bestPenalty < 0 || p < bestPenalty {
			bestPenalty = p
			bestMask = mask
		}
	}

	// Apply best mask
	m.applyMask(bestMask, reserved)
	m.writeFormat(bestMask)

	return m.modules
}

// isTerminal returns true if stdout is a terminal (not piped).
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// printQR generates and prints a QR code to the terminal using Unicode half-blocks.
// Silently does nothing if the text is too long or generation fails.
func printQR(text string) {
	modules := generateQR(text)
	if modules == nil {
		return
	}

	size := len(modules)
	// Add 2-module quiet zone on each side
	total := size + 4

	// Use Unicode half-block characters to fit 2 rows per line
	// Top half = upper row, bottom half = lower row
	// █ = both dark, ▀ = top dark/bottom light, ▄ = top light/bottom dark, ' ' = both light
	get := func(r, c int) bool {
		// Quiet zone is light
		r -= 2
		c -= 2
		if r < 0 || r >= size || c < 0 || c >= size {
			return false
		}
		return modules[r][c]
	}

	fmt.Println()
	for r := 0; r < total; r += 2 {
		fmt.Print("  ") // indent
		for c := 0; c < total; c++ {
			top := get(r, c)
			bot := get(r+1, c)
			switch {
			case top && bot:
				fmt.Print("█")
			case top && !bot:
				fmt.Print("▀")
			case !top && bot:
				fmt.Print("▄")
			default:
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}
}
