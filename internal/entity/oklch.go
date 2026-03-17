package entity

import "math"

// OKLCH represents a color in the OKLCH perceptual color space.
// L is lightness (0–1), C is chroma (0+), and H is hue angle in degrees (0–360).
type OKLCH struct {
	L float64
	C float64
	H float64
}

// SRGBToOKLCH converts an sRGB color (each channel 0–255) to OKLCH.
// The conversion path is sRGB → Linear RGB → OKLab → OKLCH.
func SRGBToOKLCH(r, g, b uint8) OKLCH {
	// sRGB → Linear RGB (inverse gamma).
	lr := srgbToLinear(float64(r) / 255.0)
	lg := srgbToLinear(float64(g) / 255.0)
	lb := srgbToLinear(float64(b) / 255.0)

	// Linear RGB → LMS (via Oklab's M1 matrix).
	l := 0.4122214708*lr + 0.5363325363*lg + 0.0514459929*lb
	m := 0.2119034982*lr + 0.6806995451*lg + 0.1073969566*lb
	s := 0.0883024619*lr + 0.2817188376*lg + 0.6299787005*lb

	// Cube root (non-linear compression).
	lc := math.Cbrt(l)
	mc := math.Cbrt(m)
	sc := math.Cbrt(s)

	// LMS' → OKLab (via M2 matrix).
	labL := 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	labA := 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	labB := 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc

	// OKLab → OKLCH (Cartesian → polar).
	c := math.Sqrt(labA*labA + labB*labB)
	h := math.Atan2(labB, labA) * (180.0 / math.Pi)
	if h < 0 {
		h += 360.0
	}

	return OKLCH{L: labL, C: c, H: h}
}

// srgbToLinear applies the inverse sRGB gamma transfer function.
func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}
