// Copyright 2026 Carlos Munoz and the Folio Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import (
	"fmt"

	"github.com/carlos7ags/folio/core"
)

// decodeTIFFPredictor reverses horizontal sample differences, modulo the
// component depth. Rows are byte-aligned; padding bits are not samples.
func decodeTIFFPredictor(data []byte, parms *core.PdfDictionary) ([]byte, error) {
	positiveInt := func(key string, fallback int) (int, error) {
		obj := parms.Get(key)
		if obj == nil {
			return fallback, nil
		}
		num, ok := obj.(*core.PdfNumber)
		if !ok {
			return 0, fmt.Errorf("invalid TIFF %s", key)
		}
		value, ok := num.IntValueChecked()
		if !ok || value <= 0 {
			return 0, fmt.Errorf("invalid TIFF %s", key)
		}
		return value, nil
	}
	colors, err := positiveInt("Colors", 1)
	if err != nil {
		return nil, err
	}
	columns, err := positiveInt("Columns", 1)
	if err != nil {
		return nil, err
	}
	bits, err := positiveInt("BitsPerComponent", 8)
	if err != nil {
		return nil, err
	}
	switch bits {
	case 1, 2, 4, 8, 16:
	default:
		return nil, fmt.Errorf("unsupported TIFF BitsPerComponent %d", bits)
	}
	maxInt := int(^uint(0) >> 1)
	if columns > maxInt/colors {
		return nil, fmt.Errorf("TIFF row sample count overflows")
	}
	samples := columns * colors
	if samples > (maxInt-7)/bits {
		return nil, fmt.Errorf("TIFF row bit count overflows")
	}
	rowBytes := (samples*bits + 7) / 8
	if len(data)%rowBytes != 0 {
		return nil, fmt.Errorf("truncated TIFF predictor row: %d bytes, row size %d", len(data), rowBytes)
	}

	result := append([]byte(nil), data...)
	for offset := 0; offset < len(result); offset += rowBytes {
		row := result[offset : offset+rowBytes]
		for sample := colors; sample < samples; sample++ {
			pos, left := sample*bits, (sample-colors)*bits
			switch bits {
			case 8:
				row[sample] += row[sample-colors]
			case 16:
				i, j := pos/8, left/8
				value := uint16(row[i])<<8 | uint16(row[i+1])
				value += uint16(row[j])<<8 | uint16(row[j+1])
				row[i], row[i+1] = byte(value>>8), byte(value)
			default:
				mask := byte((1 << bits) - 1)
				shift, leftShift := 8-bits-pos%8, 8-bits-left%8
				value := (row[pos/8] >> shift) + (row[left/8] >> leftShift)
				row[pos/8] = row[pos/8]&^(mask<<shift) | (value&mask)<<shift
			}
		}
	}
	return result, nil
}
