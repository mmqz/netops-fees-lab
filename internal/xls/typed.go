// typed.go — typed cell access for the convert command (issue #7).
//
// PATCHED(netops-fees-lab #7): upstream exposes only Row.Col(i) string —
// dates came out as hardcoded "2006.01" text (day precision lost), numbers
// as strings, merged-cell awareness missing entirely. This file adds the
// typed layer the fidelity contract of the issue needs:
//
//	TypedCell(i) → one of {number, date, text, bool, error, empty} with the
//	               raw double preserved (dates keep their full serial);
//	IsDateFormat(xf) → the XF/FORMAT table decides date-ness the same way
//	                   Excel renders it (built-in date ids + custom format
//	                   strings containing date/time letters).
package xls

import (
	"encoding/binary"
	"math"
	"strings"
	"time"
)

// CellType classifies a TypedCell.
type CellType int

const (
	CellEmpty  CellType = iota // truly absent cell
	CellBlank                  // present but blank (BLANK/MULBLANK)
	CellNumber                 // numeric, non-date
	CellDate                   // numeric, date-formatted (serial preserved)
	CellText                   // string cell / formula string result
	CellBool                   // boolean formula result
	CellError                  // error formula result
)

// TypedCell is the type-aware cell value.
type TypedCell struct {
	Type   CellType
	Number float64 // CellNumber / CellDate (the raw Excel serial for dates)
	Text   string  // CellText
	Bool   bool    // CellBool
	Err    byte    // CellError (the Excel error code)
}

// IsDate reports the XF format behind xfIdx as a date/time format: the
// built-in date format ids, or a custom (>=164) format string that carries
// date/time letters and is not a pure elapsed-time ([h]) format.
func (w *WorkBook) IsDateFormat(xfIdx uint16) bool {
	if w == nil || int(xfIdx) >= len(w.Xfs) {
		return false
	}
	fNo := w.Xfs[xfIdx].formatNo()
	switch {
	case fNo >= 14 && fNo <= 22, fNo >= 27 && fNo <= 36, fNo >= 45 && fNo <= 47, fNo >= 50 && fNo <= 58:
		return true // built-in date/time formats of Excel
	case fNo >= 164:
		if f, ok := w.Formats[uint16(fNo)]; ok && f != nil {
			return formatStringLooksLikeDate(f.str)
		}
	}
	return false
}

// formatStringLooksLikeDate mirrors Excel's rule: a number format is a
// date format when its non-escaped body contains m/d/y/h/s letters. The
// elapsed-time forms ([h], [m], [s]) are durations, not dates.
func formatStringLooksLikeDate(f string) bool {
	var sb strings.Builder
	for i := 0; i < len(f); i++ {
		c := f[i]
		if c == '\\' && i+1 < len(f) { // escaped literal: skip both bytes
			i++
			continue
		}
		if c == '"' { // literal run: skip to the closing quote
			i++
			for i < len(f) && f[i] != '"' {
				i++
			}
			continue
		}
		sb.WriteByte(c)
	}
	body := sb.String()
	if strings.ContainsRune(body, '[') { // elapsed-time/duration formats
		return false
	}
	for _, c := range body {
		switch c {
		case 'm', 'M', 'd', 'D', 'y', 'Y', 'h', 'H', 's', 'S':
			return true
		}
	}
	return false
}

// TypedCell returns the typed value of the i-th column of the row
// (CellEmpty when the sheet never wrote the cell).
func (r *Row) TypedCell(i int) TypedCell {
	if r == nil {
		return TypedCell{Type: CellEmpty}
	}
	serial := uint16(i)
	if ch, ok := r.cols[serial]; ok {
		return typedOf(r.wb, ch)
	}
	for _, v := range r.cols {
		if v.FirstCol() <= serial && serial <= v.LastCol() {
			if mc, ok := v.(*MulrkCol); ok {
				k := int(serial - mc.FirstCol())
				if k >= 0 && k < len(mc.Xfrks) {
					return typedOfXfRk(r.wb, mc.Xfrks[k])
				}
			}
			if _, ok := v.(*MulBlankCol); ok {
				return TypedCell{Type: CellBlank}
			}
		}
	}
	return TypedCell{Type: CellEmpty}
}

// typedOf decodes one content handler cell.
func typedOf(wb *WorkBook, ch contentHandler) TypedCell {
	switch c := ch.(type) {
	case *RkCol:
		return typedOfXfRk(wb, c.Xfrk)
	case *MulrkCol:
		return typedOfXfRk(wb, c.Xfrks[0])
	case *NumberCol:
		return typedNumber(wb, c.Index, c.Float)
	case *LabelsstCol:
		if int(c.Sst) < len(wb.sst) {
			return TypedCell{Type: CellText, Text: wb.sst[c.Sst]}
		}
		return TypedCell{Type: CellText}
	case *labelCol:
		return TypedCell{Type: CellText, Text: c.Str}
	case *FormulaCol:
		return typedFormula(wb, c)
	case *BlankCol:
		return TypedCell{Type: CellBlank}
	case *MulBlankCol:
		return TypedCell{Type: CellBlank}
	}
	return TypedCell{Type: CellEmpty}
}

// typedOfXfRk decodes an RK-compressed number with its XF format.
func typedOfXfRk(wb *WorkBook, xf XfRk) TypedCell {
	i, f, isFloat := xf.Rk.number()
	if !isFloat {
		f = float64(i)
	}
	return typedNumber(wb, xf.Index, f)
}

// typedNumber applies date-ness to a raw double via its XF.
func typedNumber(wb *WorkBook, xfIdx uint16, f float64) TypedCell {
	if wb.IsDateFormat(xfIdx) {
		return TypedCell{Type: CellDate, Number: f}
	}
	return TypedCell{Type: CellNumber, Number: f}
}

// typedFormula decodes the cached result of a FORMULA record: the first 8
// bytes hold the value (IEEE double, or the FF-pattern specials — string,
// bool, error, empty — per MS-XLS; a normal double colliding with the
// special pattern would carry an exponent ~1e-240, impossible in ledger
// data).
func typedFormula(wb *WorkBook, c *FormulaCol) TypedCell {
	res := c.Header.Result
	if len(res) >= 8 && res[6] == 0xFF {
		switch res[7] {
		case 0x00: // string — captured by the STRING record after the FORMULA
			return TypedCell{Type: CellText, Text: c.Str}
		case 0x01: // boolean — value in byte 0
			return TypedCell{Type: CellBool, Bool: res[0] != 0}
		case 0x02: // error — code in byte 0
			return TypedCell{Type: CellError, Err: res[0]}
		case 0x03: // empty
			return TypedCell{Type: CellBlank}
		}
	}
	num := math.Float64frombits(binary.LittleEndian.Uint64(res[:8]))
	return typedNumber(wb, c.Header.IndexXf, num)
}

// DateValue converts an Excel serial to a time.Time using the workbook's
// date mode (1900/1904) — full precision, the fix of the hardcoded
// "2006.01" truncation upstream String() had for date cells.
func (w *WorkBook) DateValue(serial float64) time.Time {
	return timeFromExcelTime(serial, w.dateMode == 1)
}
