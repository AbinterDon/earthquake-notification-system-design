// Package geo provides geohash-based spatial indexing utilities.
//
// We use geohash precision 5 (≈ 4.9 km × 4.9 km cells) as the default
// resolution for the location index. The PDF recommends S2/H3 for production;
// geohash is used here because it is pure-Go and requires no CGO.
package geo

import (
	"math"

	"github.com/mmcloughlin/geohash"
)

// DefaultPrecision is the geohash character length used for cell indexing.
// Precision 5 ≈ 4.9 km × 4.9 km.
const DefaultPrecision uint = 5

// EncodeCell encodes a lat/lng pair into a geohash cell string.
func EncodeCell(lat, lng float64) string {
	return geohash.EncodeWithPrecision(lat, lng, DefaultPrecision)
}

// CellCenter decodes a cell string to its centroid coordinates.
func CellCenter(cell string) (lat, lng float64) {
	return geohash.DecodeCenter(cell)
}

// CellsInRadius returns every geohash cell whose centroid lies within
// radiusKm of (lat, lng), plus a one-cell margin to avoid boundary
// false-negatives.
//
// Algorithm: BFS outward from the center cell using the 8-neighbor
// adjacency built into the geohash library.
func CellsInRadius(lat, lng, radiusKm float64) []string {
	// Add one cell's width as margin to reduce boundary misses.
	searchRadius := radiusKm + cellWidthKm(DefaultPrecision)

	center := geohash.EncodeWithPrecision(lat, lng, DefaultPrecision)
	seen := map[string]struct{}{center: {}}
	result := []string{center}
	frontier := []string{center}

	for len(frontier) > 0 {
		var next []string
		for _, cell := range frontier {
			for _, nb := range geohash.Neighbors(cell) {
				if _, exists := seen[nb]; exists {
					continue
				}
				seen[nb] = struct{}{}
				nbLat, nbLng := geohash.DecodeCenter(nb)
				if HaversineKm(lat, lng, nbLat, nbLng) <= searchRadius {
					result = append(result, nb)
					next = append(next, nb)
				}
			}
		}
		frontier = next
	}
	return result
}

// HaversineKm computes the great-circle distance in kilometres between two
// latitude/longitude points.
func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLng := (lng2 - lng1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// MagnitudeToRadius returns an approximate maximum felt-radius in km for a
// given Richter magnitude. Used by the Broadcast Service to bound the cell
// search before applying per-user distance filters.
func MagnitudeToRadius(magnitude float64) float64 {
	// Simplified empirical model:  r ≈ 10^(0.5*M - 0.8)  km (capped at 1000)
	r := math.Pow(10, 0.5*magnitude-0.8)
	if r < 50 {
		r = 50
	}
	if r > 1000 {
		r = 1000
	}
	return r
}

// cellWidthKm returns the approximate side length (in km) for a given
// geohash precision level.
func cellWidthKm(precision uint) float64 {
	widths := map[uint]float64{
		1: 5000, 2: 1250, 3: 156, 4: 39, 5: 4.9, 6: 1.2,
	}
	if w, ok := widths[precision]; ok {
		return w
	}
	return 4.9
}
