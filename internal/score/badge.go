package score

import "fmt"

var bandColors = map[Band]string{
	Good: "#2e7d32", OK: "#f9a825", Poor: "#c62828", "": "#97a5a0",
}

// BadgeSVG emits a shields.io flat-format badge:
// [ label | value ] with the band color on the right segment.
func BadgeSVG(label, value string, band Band) string {
	color := bandColors[band]
	if color == "" {
		color = bandColors[""]
	}
	lw, rw := textW(label), textW(value)
	lpad, rpad := 10, 10
	lseg, rseg := lw+2*lpad, rw+2*rpad
	total := lseg + rseg
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">
<title>%s: %s</title>
<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>
<g clip-path="url(#r)">
<rect width="%d" height="20" fill="#555"/>
<rect x="%d" width="%d" height="20" fill="%s"/>
<rect width="%d" height="20" fill="url(#s)"/>
</g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110">
<text x="%d" y="140" transform="scale(.1)" fill="#010101" fill-opacity=".3">%s</text>
<text x="%d" y="140" transform="scale(.1)">%s</text>
<text x="%d" y="140" transform="scale(.1)" fill="#010101" fill-opacity=".3">%s</text>
<text x="%d" y="140" transform="scale(.1)">%s</text>
</g></svg>`,
		total, esc(label), esc(value), esc(label), esc(value),
		total, lseg, lseg, rseg, color, total,
		lseg*50, esc(label), lseg*50, esc(label),
		lseg*100+rseg*50, esc(value), lseg*100+rseg*50, esc(value))
}

func textW(s string) int {
	// shields uses fixed-width glyph metrics at 11px Verdana
	w := 0
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			w += 7
		case r >= '0' && r <= '9':
			w += 7
		case r == '.' || r == ':' || r == ' ':
			w += 4
		default:
			w += 6
		}
	}
	return w
}

func esc(s string) string {
	r := map[rune]string{'&': "&amp;", '<': "&lt;", '>': "&gt;", '"': "&quot;"}
	out := ""
	for _, c := range s {
		if e, ok := r[c]; ok {
			out += e
		} else {
			out += string(c)
		}
	}
	return out
}
