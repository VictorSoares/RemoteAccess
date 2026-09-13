package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// Generate a high-end Fluent/Windows 11 styled icon at size x size
func generateMasterIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)

	// Background squircle parameters: 8% safe margin for Windows 11 Explorer tile
	margin := s * 0.08
	cardSize := s - margin*2
	radius := cardSize * 0.225
	cx := s / 2
	cy := s / 2

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var totalR, totalG, totalB, totalA float64
			samples := 4
			step := 1.0 / float64(samples)

			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)*step
					py := float64(y) + (float64(sy)+0.5)*step

					// 1. Check Squircle Distance
					dx := math.Abs(px-cx) - (cardSize/2 - radius)
					dy := math.Abs(py-(cy+s*0.01)) - (cardSize/2 - radius)

					dist := 0.0
					if dx > 0 && dy > 0 {
						dist = math.Sqrt(dx*dx+dy*dy) - radius
					} else if dx > 0 {
						dist = dx - radius
					} else if dy > 0 {
						dist = dy - radius
					} else {
						dist = math.Max(dx, dy) - radius
					}

					// Ambient soft drop shadow below tile
					shadowDy := math.Abs(py-(cy+s*0.035)) - (cardSize/2 - radius)
					shadowDist := 0.0
					if dx > 0 && shadowDy > 0 {
						shadowDist = math.Sqrt(dx*dx+shadowDy*shadowDy) - radius
					} else if dx > 0 {
						shadowDist = dx - radius
					} else if shadowDy > 0 {
						shadowDist = shadowDy - radius
					} else {
						shadowDist = math.Max(dx, shadowDy) - radius
					}

					r, g, b, a := 0.0, 0.0, 0.0, 0.0

					if shadowDist <= s*0.04 {
						shAlpha := 0.32 * math.Max(0, 1.0-shadowDist/(s*0.04))
						r, g, b, a = 0, 0, 0, shAlpha
					}

					// Main Squircle Body
					if dist <= 0 {
						normY := (py - (cy - cardSize/2)) / cardSize
						normX := (px - (cx - cardSize/2)) / cardSize
						t := (normX*0.35 + normY*0.65)
						t = math.Max(0, math.Min(1, t))

						// Vibrant Azure to Royal Indigo gradient
						bodyR := 14.0*(1.0-t) + 30.0*t + 12.0*(1.0-normY)
						bodyG := 138.0*(1.0-t) + 64.0*t
						bodyB := 250.0*(1.0-t) + 215.0*t

						// Inner border glow / highlight (top rim)
						if dist > -s*0.012 && normY < 0.65 {
							rimT := (dist + s*0.012) / (s*0.012)
							bodyR = bodyR*(1.0-rimT*0.45) + 255.0*(rimT*0.45)
							bodyG = bodyG*(1.0-rimT*0.45) + 255.0*(rimT*0.45)
							bodyB = bodyB*(1.0-rimT*0.45) + 255.0*(rimT*0.45)
						}

						// Normalized coordinates in [0..1] inside the card
						bx := (px - (cx - cardSize*0.5)) / cardSize
						by := (py - (cy - cardSize*0.5)) / cardSize

						if inLightningBolt(bx, by) {
							// Lightning bolt with pure clean white & slight warm glow
							boltT := by
							boltR := 255.0
							boltG := 255.0*(1.0-boltT*0.08) + 246.0*(boltT*0.08)
							boltB := 255.0*(1.0-boltT*0.18) + 230.0*(boltT*0.18)
							bodyR, bodyG, bodyB = boltR, boltG, boltB
						} else if inLightningShadow(bx, by) {
							// Soft bolt drop shadow
							bodyR *= 0.62
							bodyG *= 0.62
							bodyB *= 0.72
						}

						r, g, b, a = bodyR, bodyG, bodyB, 1.0
					}

					totalR += r * a
					totalG += g * a
					totalB += b * a
					totalA += a
				}
			}

			count := float64(samples * samples)
			finA := totalA / count
			if finA > 0.001 {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8(math.Min(255, (totalR/count)/finA*finA)),
					G: uint8(math.Min(255, (totalG/count)/finA*finA)),
					B: uint8(math.Min(255, (totalB/count)/finA*finA)),
					A: uint8(math.Min(255, finA*255)),
				})
			}
		}
	}
	return img
}

func inLightningShadow(x, y float64) bool {
	return inLightningBolt(x-0.02, y-0.02)
}

func inLightningBolt(x, y float64) bool {
	poly := [][2]float64{
		{0.575, 0.175}, // Top tip
		{0.320, 0.515}, // Left indent
		{0.485, 0.515}, // Inner waist left
		{0.415, 0.825}, // Bottom tip
		{0.700, 0.455}, // Right indent
		{0.535, 0.455}, // Inner waist right
	}

	return pointInPoly(x, y, poly)
}

func pointInPoly(x, y float64, poly [][2]float64) bool {
	inside := false
	j := len(poly) - 1
	for i := 0; i < len(poly); i++ {
		xi, yi := poly[i][0], poly[i][1]
		xj, yj := poly[j][0], poly[j][1]

		intersect := ((yi > y) != (yj > y)) && (x < (xj-xi)*(y-yi)/(yj-yi)+xi)
		if intersect {
			inside = !inside
		}
		j = i
	}
	return inside
}

func main() {
	master := generateMasterIcon(256)
	f, err := os.Create("d:\\Documentos\\GitHub\\RemoteAccess\\winres\\icon.png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	_ = png.Encode(f, master)
}
