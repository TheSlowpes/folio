// Copyright 2026 Carlos Munoz and the Folio Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/carlos7ags/folio/core"
)

func tiffParms(colors, columns, bits int) *core.PdfDictionary {
	parms := core.NewPdfDictionary()
	for key, value := range map[string]int{
		"Predictor": 2, "Colors": colors, "Columns": columns, "BitsPerComponent": bits,
	} {
		parms.Set(key, core.NewPdfInteger(value))
	}
	return parms
}

func TestTIFFPredictor(t *testing.T) {
	tests := []struct {
		name                  string
		colors, columns, bits int
		encoded, want         []byte
	}{
		{"gray rows", 1, 4, 8, []byte{32, 32, 32, 32, 64, 32, 32, 32}, []byte{32, 64, 96, 128, 64, 96, 128, 160}},
		{"RGB wrap and rows", 3, 2, 8, []byte{250, 10, 100, 10, 250, 200, 1, 2, 3, 4, 5, 6}, []byte{250, 10, 100, 4, 4, 44, 1, 2, 3, 5, 7, 9}},
		{"CMYK", 4, 2, 8, []byte{1, 2, 3, 4, 10, 20, 30, 40}, []byte{1, 2, 3, 4, 11, 22, 33, 44}},
		{"single pixel", 3, 1, 8, []byte{1, 2, 3}, []byte{1, 2, 3}},
		{"one bit padding and rows", 1, 5, 1, []byte{0xef, 0x97}, []byte{0xb7, 0xe7}},
		{"two bit RGB padding", 3, 3, 2, []byte{0x6c, 0x6c, 0x7f}, []byte{0x6d, 0xd3, 0xbf}},
		{"four bit RGB padding", 3, 3, 4, []byte{0xf1, 0x23, 0xf4, 0x1f, 0x8a}, []byte{0xf1, 0x22, 0x06, 0x3f, 0xea}},
		{"sixteen bit carry wrap rows", 1, 3, 16, []byte{0, 255, 0, 2, 255, 255, 255, 255, 0, 2, 0, 255}, []byte{0, 255, 1, 1, 1, 0, 255, 255, 0, 1, 1, 0}},
		{"sixteen bit RGB", 3, 2, 16, []byte{0, 255, 255, 255, 1, 0, 0, 1, 0, 2, 255, 255}, []byte{0, 255, 255, 255, 1, 0, 1, 0, 0, 1, 0, 255}},
		{"empty", 1, 4, 8, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := bytes.Clone(tt.encoded)
			got, err := applyPredictor(tt.encoded, tiffParms(tt.colors, tt.columns, tt.bits))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("got %x, want %x", got, tt.want)
			}
			if !bytes.Equal(tt.encoded, original) {
				t.Fatal("decoder mutated encoded input")
			}
		})
	}
}

func TestTIFFPredictorDefaults(t *testing.T) {
	parms := core.NewPdfDictionary()
	parms.Set("Predictor", core.NewPdfInteger(2))
	got, err := applyPredictor([]byte{10, 20}, parms)
	if err != nil || !bytes.Equal(got, []byte{10, 20}) {
		t.Fatalf("defaults: %x, %v", got, err)
	}
}

func TestTIFFPredictorInvalidParameters(t *testing.T) {
	for _, key := range []string{"Colors", "Columns", "BitsPerComponent", "Predictor"} {
		for name, value := range map[string]core.PdfObject{
			"zero": core.NewPdfInteger(0), "negative": core.NewPdfInteger(-1),
			"fraction": core.NewPdfReal(1.5), "string": core.NewPdfLiteralString("8"),
			"NaN": core.NewPdfReal(math.NaN()), "infinite": core.NewPdfReal(math.Inf(1)),
			"int boundary": core.NewPdfReal(math.Exp2(63)),
		} {
			t.Run(key+"/"+name, func(t *testing.T) {
				parms := tiffParms(1, 4, 8)
				parms.Set(key, value)
				if _, err := applyPredictor([]byte{1, 2, 3, 4}, parms); err == nil {
					t.Fatal("expected parameter error")
				}
			})
		}
	}
	for _, bits := range []int{3, 7, 12, 32} {
		t.Run(fmt.Sprintf("unsupported bits %d", bits), func(t *testing.T) {
			if _, err := applyPredictor([]byte{0}, tiffParms(1, 1, bits)); err == nil {
				t.Fatal("expected unsupported depth error")
			}
		})
	}
	for _, dims := range [][3]int{
		{1, 4, 8}, {3, 2, 16}, {1, 9, 1},
		{1 << 30, 1 << 30, 16}, {1 << 30, 1 << 30, 8},
		{1 << 30, 1 << 30, 1},
	} {
		t.Run(fmt.Sprint(dims), func(t *testing.T) {
			if _, err := applyPredictor([]byte{0}, tiffParms(dims[0], dims[1], dims[2])); err == nil {
				t.Fatal("expected truncated row or overflow error")
			}
		})
	}
	maxInt := int(^uint(0) >> 1)
	if _, err := applyPredictor(nil, tiffParms(maxInt/2, 3, 8)); err == nil {
		t.Fatal("expected sample count overflow")
	}
	if _, err := applyPredictor(nil, tiffParms(1, maxInt/8, 16)); err == nil {
		t.Fatal("expected row bit count overflow")
	}
}

func TestFlateTIFFPredictor(t *testing.T) {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, err := zw.Write([]byte{32, 32, 32, 32, 64, 32, 32, 32})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, array := range []bool{false, true} {
		t.Run(fmt.Sprintf("array=%v", array), func(t *testing.T) {
			dict := core.NewPdfDictionary()
			parms := tiffParms(1, 4, 8)
			dict.Set("Filter", core.NewPdfName("FlateDecode"))
			if array {
				dict.Set("Filter", core.NewPdfArray(core.NewPdfName("FlateDecode")))
				dict.Set("DecodeParms", core.NewPdfArray(parms))
			} else {
				dict.Set("DecodeParms", parms)
			}
			got, err := decompressStreamWithLimit(compressed.Bytes(), dict, 1024)
			if err != nil || !bytes.Equal(got, []byte{32, 64, 96, 128, 64, 96, 128, 160}) {
				t.Fatalf("decode: %v, %v", got, err)
			}
			dict.Set("Length", core.NewPdfInteger(compressed.Len()))
			input := append([]byte("stream\n"), compressed.Bytes()...)
			input = append(input, []byte("\nendstream")...)
			res := newResolver(input, nil, newMemoryTracker(MemoryLimits{}), StrictnessStrict)
			stream := core.NewPdfStream(nil)
			stream.Dict = dict
			decoded, err := res.resolveStream(stream, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded.Data, got) || decoded.Dict.Get("DecodeParms") != nil || decoded.Dict.Get("Filter") != nil {
				t.Fatal("predictor must be reversed before removing filter metadata")
			}
			parms.Set("Columns", core.NewPdfInteger(3))
			if _, err := decompressStreamWithLimit(compressed.Bytes(), dict, 1024); err == nil || !strings.Contains(err.Error(), "truncated TIFF") {
				t.Fatalf("predictor error was lost: %v", err)
			}
			if _, err := res.resolveStream(stream, 0); err == nil {
				t.Fatal("resolver lost predictor error")
			}
		})
	}
	t.Run("filter chain", func(t *testing.T) {
		dict := core.NewPdfDictionary()
		dict.Set("Filter", core.NewPdfArray(core.NewPdfName("ASCIIHexDecode"), core.NewPdfName("FlateDecode")))
		dict.Set("DecodeParms", core.NewPdfArray(core.NewPdfNull(), tiffParms(1, 4, 8)))
		data := []byte(hex.EncodeToString(compressed.Bytes()) + ">")
		got, err := decompressStreamWithLimit(data, dict, 1024)
		if err != nil || !bytes.Equal(got, []byte{32, 64, 96, 128, 64, 96, 128, 160}) {
			t.Fatalf("chain decode: %v, %v", got, err)
		}
		for _, invalid := range []core.PdfObject{
			core.NewPdfArray(), core.NewPdfArray(core.NewPdfNull()),
			core.NewPdfArray(core.NewPdfNull(), core.NewPdfInteger(2)), tiffParms(1, 4, 8),
		} {
			dict.Set("DecodeParms", invalid)
			if _, err := decompressStreamWithLimit(data, dict, 1024); err == nil {
				t.Fatal("invalid filter parameters accepted")
			}
		}
	})
}

// Exercise the public import path: decoding failures in indirect image
// resources must not become successful imports with unresolved references.
func TestImportTIFFPredictorError(t *testing.T) {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write([]byte{32, 32, 32, 32}); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, parms string
	}{
		{"truncated row", "/Predictor 2 /Columns 3"},
		{"unsupported depth", "/Predictor 2 /BitsPerComponent 12 /Columns 4"},
		{"unsupported predictor", "/Predictor 3 /Columns 4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := [][]byte{
				[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
				[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
				[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /XObject << /Im0 4 0 R >> >> >>"),
				append(append([]byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 4 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << %s >> /Length %d >>\nstream\n", test.parms, compressed.Len())), compressed.Bytes()...), []byte("\nendstream")...),
			}
			var pdf bytes.Buffer
			pdf.WriteString("%PDF-1.7\n")
			offsets := make([]int, len(objects))
			for i, obj := range objects {
				offsets[i] = pdf.Len()
				fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
			}
			xref := pdf.Len()
			fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
			for _, offset := range offsets {
				fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
			}
			fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
			r, err := ParseWithOptions(pdf.Bytes(), ReadOptions{Strictness: StrictnessStrict})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ExtractPageImport(r, 0); err == nil || !strings.Contains(err.Error(), "predictor") {
				t.Fatalf("import lost predictor error: %v", err)
			}
		})
	}
}
