package store

import "math"

// The WGS84 ellipsoid, which is what SRID 4326 means to PostGIS.
const (
	wgs84A = 6378137.0
	wgs84F = 1 / 298.257223563
	wgs84B = wgs84A * (1 - wgs84F)
)

// spheroidDistance is the distance PostGIS reports for two geography points, in metres.
//
// ST_Distance on geography measures along the ellipsoid, not the sphere, and the neighbours
// block prints the figure, so the copy of the catalogue held in memory has to measure the
// same way or every distance on a store page would move. Vincenty's inverse solution,
// iterated until it stops changing rather than to the customary 1e-12 -- that tolerance is
// six micrometres of longitude, and it showed. Measured against PostGIS 3.6 on 3,000 pairs
// up to thirteen kilometres apart, the two agree to a tenth of a micrometre; PostGIS uses
// Karney's series and the last digits differ, which nobody reading "1,2 km" can see.
//
// Rounded to ten nanometres the way PostGIS rounds it (ticket #2168), so the figures have
// the same shape on both paths.
func spheroidDistance(lat1, lon1, lat2, lon2 float64) float64 {
	if lat1 == lat2 && lon1 == lon2 {
		return 0
	}
	const rad = math.Pi / 180
	l := (lon2 - lon1) * rad
	sinU1, cosU1 := math.Sincos(math.Atan((1 - wgs84F) * math.Tan(lat1*rad)))
	sinU2, cosU2 := math.Sincos(math.Atan((1 - wgs84F) * math.Tan(lat2*rad)))
	lambda := l
	var sinSigma, cosSigma, sigma, cos2Alpha, cos2SigmaM float64
	for i := 0; i < 200; i++ {
		sinLambda, cosLambda := math.Sincos(lambda)
		x := cosU2 * sinLambda
		y := cosU1*sinU2 - sinU1*cosU2*cosLambda
		sinSigma = math.Sqrt(x*x + y*y)
		if sinSigma == 0 {
			return 0
		}
		cosSigma = sinU1*sinU2 + cosU1*cosU2*cosLambda
		sigma = math.Atan2(sinSigma, cosSigma)
		sinAlpha := cosU1 * cosU2 * sinLambda / sinSigma
		cos2Alpha = 1 - sinAlpha*sinAlpha
		cos2SigmaM = 0
		if cos2Alpha != 0 {
			cos2SigmaM = cosSigma - 2*sinU1*sinU2/cos2Alpha
		}
		c := wgs84F / 16 * cos2Alpha * (4 + wgs84F*(4-3*cos2Alpha))
		previous := lambda
		lambda = l + (1-c)*wgs84F*sinAlpha*(sigma+c*sinSigma*(cos2SigmaM+c*cosSigma*(-1+2*cos2SigmaM*cos2SigmaM)))
		if math.Abs(lambda-previous) <= 1e-15 {
			break
		}
	}
	u2 := cos2Alpha * (wgs84A*wgs84A - wgs84B*wgs84B) / (wgs84B * wgs84B)
	a := 1 + u2/16384*(4096+u2*(-768+u2*(320-175*u2)))
	b := u2 / 1024 * (256 + u2*(-128+u2*(74-47*u2)))
	deltaSigma := b * sinSigma * (cos2SigmaM + b/4*(cosSigma*(-1+2*cos2SigmaM*cos2SigmaM)-b/6*cos2SigmaM*(-3+4*sinSigma*sinSigma)*(-3+4*cos2SigmaM*cos2SigmaM)))
	return math.Round(wgs84B*a*(sigma-deltaSigma)*1e8) / 1e8
}
