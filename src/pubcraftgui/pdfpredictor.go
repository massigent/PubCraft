package main

import "fmt"

// Decode the predictors used by Flate xref/object streams. Explicit limits keep
// malformed dimensions from allocating unbounded memory or overflowing indices.
func decodePDFPredictor(data []byte, predictor, colors, bits, columns int) ([]byte, error) {
	if predictor <= 1 {
		return data, nil
	}
	if colors < 1 || colors > 32 || columns < 1 || columns > 1<<20 || (bits != 1 && bits != 2 && bits != 4 && bits != 8 && bits != 16) {
		return nil, fmt.Errorf("dimensioni predictor PDF non valide")
	}
	rowBytes := (colors*columns*bits + 7) / 8
	bpp := (colors*bits + 7) / 8
	if rowBytes > maxPDFStreamBytes {
		return nil, fmt.Errorf("riga predictor troppo grande")
	}
	if predictor == 2 {
		if bits != 8 || len(data)%rowBytes != 0 {
			return nil, fmt.Errorf("TIFF predictor non supportato o troncato")
		}
		out := append([]byte(nil), data...)
		for start := 0; start < len(out); start += rowBytes {
			for i := bpp; i < rowBytes; i++ {
				out[start+i] += out[start+i-bpp]
			}
		}
		return out, nil
	}
	if predictor < 10 || predictor > 15 || len(data)%(rowBytes+1) != 0 {
		return nil, fmt.Errorf("PNG predictor non valido o troncato")
	}
	out := make([]byte, 0, len(data))
	prev := make([]byte, rowBytes)
	for start := 0; start < len(data); start += rowBytes + 1 {
		filter := data[start]
		row := append([]byte(nil), data[start+1:start+1+rowBytes]...)
		for i := range row {
			var left, up, ul byte
			if i >= bpp {
				left = row[i-bpp]
				ul = prev[i-bpp]
			}
			up = prev[i]
			switch filter {
			case 0:
			case 1:
				row[i] += left
			case 2:
				row[i] += up
			case 3:
				row[i] += byte((int(left) + int(up)) / 2)
			case 4:
				row[i] += paethPDF(left, up, ul)
			default:
				return nil, fmt.Errorf("filtro PNG predictor sconosciuto: %d", filter)
			}
		}
		out = append(out, row...)
		prev = row
	}
	return out, nil
}

func paethPDF(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}
