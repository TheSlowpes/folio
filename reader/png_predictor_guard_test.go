// Copyright 2026 Carlos Munoz and the Folio Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/carlos7ags/folio/core"
)

func TestPNGPredictorGuard(t *testing.T) {
	for predictor := 10; predictor <= 15; predictor++ {
		for key, values := range map[string][]int{
			"Colors": {2, 3, 4}, "BitsPerComponent": {1, 2, 4, 16},
		} {
			for _, value := range values {
				parms := tiffParms(1, 4, 8)
				parms.Set("Predictor", core.NewPdfInteger(predictor))
				parms.Set(key, core.NewPdfInteger(value))
				if _, err := applyPredictor([]byte{1, 32, 32, 32, 32}, parms); err == nil {
					t.Fatalf("predictor %d accepted %s=%d", predictor, key, value)
				}
			}
		}
	}
	for _, key := range []string{"Colors", "BitsPerComponent", "Columns"} {
		for _, value := range []core.PdfObject{
			core.NewPdfInteger(0), core.NewPdfInteger(-1), core.NewPdfReal(1.5),
			core.NewPdfLiteralString("1"), core.NewPdfReal(math.NaN()),
			core.NewPdfReal(math.Inf(1)), core.NewPdfReal(math.Exp2(63)),
		} {
			parms := tiffParms(1, 4, 8)
			parms.Set("Predictor", core.NewPdfInteger(15))
			parms.Set(key, value)
			if _, err := applyPredictor([]byte{1, 32, 32, 32, 32}, parms); err == nil {
				t.Fatalf("accepted invalid PNG %s: %v", key, value)
			}
		}
	}
	parms := tiffParms(1, 4, 8)
	parms.Set("Predictor", core.NewPdfInteger(15))
	for _, data := range [][]byte{{1}, {1, 2, 3, 4, 5, 1}, {5, 1, 2, 3, 4}} {
		if _, err := applyPredictor(data, parms); err == nil {
			t.Fatalf("accepted malformed row %v", data)
		}
	}
	parms.Set("Columns", core.NewPdfInteger(int(^uint(0)>>1)))
	if _, err := applyPredictor([]byte{0}, parms); err == nil {
		t.Fatal("accepted overflowing row size")
	}
	defaults := core.NewPdfDictionary()
	defaults.Set("Predictor", core.NewPdfInteger(15))
	got, err := applyPredictor([]byte{0, 32, 1, 64}, defaults)
	if err != nil || !bytes.Equal(got, []byte{32, 64}) {
		t.Fatalf("default grayscale layout: %v, %v", got, err)
	}
}

func TestImportPNGPredictorGuard(t *testing.T) {
	rgb := []byte{
		255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 0,
		0, 255, 255, 255, 0, 255, 64, 128, 192, 192, 128, 64,
	}
	var predictedRGB []byte
	for row := range 2 {
		predictedRGB = append(predictedRGB, 1)
		for column := range 12 {
			value := rgb[row*12+column]
			if column >= 3 {
				value -= rgb[row*12+column-3]
			}
			predictedRGB = append(predictedRGB, value)
		}
	}
	for _, test := range []struct {
		name, color, parms string
		data, want         []byte
		reject             bool
	}{
		{"RGB Sub fixture", "DeviceRGB", "/Predictor 15 /Colors 3 /BitsPerComponent 8 /Columns 4", predictedRGB, nil, true},
		{"unpredicted RGB control", "DeviceRGB", "/Predictor 1", rgb, rgb, false},
		{"grayscale Sub control", "DeviceGray", "/Predictor 15 /Colors 1 /BitsPerComponent 8 /Columns 4",
			[]byte{1, 32, 32, 32, 32, 1, 64, 32, 32, 32}, []byte{32, 64, 96, 128, 64, 96, 128, 160}, false},
		{"grayscale None control", "DeviceGray", "/Predictor 10 /Columns 4",
			[]byte{0, 32, 64, 96, 128, 0, 64, 96, 128, 160}, []byte{32, 64, 96, 128, 64, 96, 128, 160}, false},
	} {
		for _, strictness := range []Strictness{StrictnessStrict, StrictnessTolerant} {
			t.Run(fmt.Sprintf("%s/%d", test.name, strictness), func(t *testing.T) {
				var compressed bytes.Buffer
				zw := zlib.NewWriter(&compressed)
				if _, err := zw.Write(test.data); err != nil {
					t.Fatal(err)
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
				image := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 4 /Height 2 /ColorSpace /%s /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << %s >> /Length %d >>\nstream\n%s\nendstream", test.color, test.parms, compressed.Len(), compressed.Bytes())
				pdf := pageTreePDF(
					"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
					"<< /Type /Page /MediaBox [0 0 200 200] /Resources << /XObject << /Im0 4 0 R >> >> >>",
					image,
				)
				r, err := ParseWithOptions(pdf, ReadOptions{Strictness: strictness})
				if err != nil {
					t.Fatal(err)
				}
				page, err := ExtractPageImport(r, 0)
				if test.reject {
					if err == nil || !strings.Contains(err.Error(), "unsupported PNG predictor layout") {
						t.Fatalf("expected public import rejection, got %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				img := page.Resources.Get("XObject").(*core.PdfDictionary).Get("Im0").(*core.PdfStream)
				if !bytes.Equal(img.Data, test.want) || img.Dict.Get("DecodeParms") != nil {
					t.Fatalf("control import changed pixels or retained stale metadata: %v", img.Data)
				}
			})
		}
	}
}
