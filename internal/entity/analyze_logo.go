package entity

import "image"

const (
	// chromaThreshold is the minimum OKLCH chroma for a pixel to be
	// considered chromatic (colored rather than gray).
	chromaThreshold = 0.04

	// chromaticRatio is the fraction of non-transparent pixels that must be
	// chromatic for the logo to be classified as chromatic overall.
	chromaticRatio = 0.30

	// alphaThreshold is the minimum alpha value (0–255) for a pixel to be
	// included in the analysis. Pixels below this are treated as transparent.
	alphaThreshold = 10

	// hueBins is the number of histogram bins spanning the 0–360 hue range.
	hueBins = 36
)

// AnalyzeLogo examines the pixels of img and returns a color profile
// summarizing its dominant hue and lightness characteristics.
// Returns nil when the image contains no non-transparent pixels.
//
// AnalyzeLogo is a pure function with no I/O or side effects.
func AnalyzeLogo(img image.Image) *LogoColorProfile {
	bounds := img.Bounds()

	var (
		hueHist      [hueBins]int
		totalPixels  int
		chromaticPx  int
		lightnessSum float64
	)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()

			// Skip transparent pixels.
			if a>>8 < alphaThreshold {
				continue
			}

			// Convert from 16-bit premultiplied to 8-bit straight alpha.
			r8, g8, b8 := demultiply(r, g, b, a)

			oklch := SRGBToOKLCH(r8, g8, b8)
			totalPixels++
			lightnessSum += oklch.L

			if oklch.C > chromaThreshold {
				chromaticPx++
				bin := int(oklch.H / (360.0 / float64(hueBins)))
				if bin >= hueBins {
					bin = hueBins - 1
				}
				hueHist[bin]++
			}
		}
	}

	if totalPixels == 0 {
		return nil
	}

	meanLightness := lightnessSum / float64(totalPixels)
	isChromatic := float64(chromaticPx)/float64(totalPixels) >= chromaticRatio

	profile := &LogoColorProfile{
		DominantLightness: meanLightness,
		IsChromatic:       isChromatic,
	}

	if isChromatic {
		peakBin := 0
		for i := 1; i < hueBins; i++ {
			if hueHist[i] > hueHist[peakBin] {
				peakBin = i
			}
		}
		hue := (float64(peakBin) + 0.5) * (360.0 / float64(hueBins))
		profile.DominantHue = &hue
	}

	return profile
}

// demultiply converts 16-bit premultiplied RGBA values to 8-bit straight-alpha
// sRGB components.
func demultiply(r, g, b, a uint32) (uint8, uint8, uint8) {
	if a == 0 {
		return 0, 0, 0
	}
	// Convert premultiplied 16-bit to straight 8-bit.
	return uint8(r * 0xff / a), uint8(g * 0xff / a), uint8(b * 0xff / a)
}

// BestLogoURL selects the logo URL to use for color analysis.
// It returns the best HDMusicLogo by likes, falling back to MusicLogo.
// Returns an empty string when neither type is available.
func BestLogoURL(f *Fanart) string {
	if url := BestByLikes(f.HDMusicLogo); url != "" {
		return url
	}
	return BestByLikes(f.MusicLogo)
}
