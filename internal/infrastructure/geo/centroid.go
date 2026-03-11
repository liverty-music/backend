package geo

// Coordinates represents a WGS 84 geographic coordinate used internally by the geo package.
type Coordinates struct {
	Latitude  float64
	Longitude float64
}

// ResolveCentroid returns the geographic centroid for an ISO 3166-2 subdivision code.
// Returns the centroid and true if found, or zero value and false otherwise.
func ResolveCentroid(code string) (Coordinates, bool) {
	c, ok := centroids[code]
	return c, ok
}

// centroids maps ISO 3166-2 codes to approximate geographic centroids.
var centroids = map[string]Coordinates{
	"JP-01": {Latitude: 43.0642, Longitude: 141.3469}, // Hokkaido
	"JP-02": {Latitude: 40.8244, Longitude: 140.7400}, // Aomori
	"JP-03": {Latitude: 39.7036, Longitude: 141.1527}, // Iwate
	"JP-04": {Latitude: 38.2688, Longitude: 140.8721}, // Miyagi
	"JP-05": {Latitude: 39.7186, Longitude: 140.1024}, // Akita
	"JP-06": {Latitude: 38.2404, Longitude: 140.3633}, // Yamagata
	"JP-07": {Latitude: 37.7500, Longitude: 140.4678}, // Fukushima
	"JP-08": {Latitude: 36.3419, Longitude: 140.4468}, // Ibaraki
	"JP-09": {Latitude: 36.5658, Longitude: 139.8836}, // Tochigi
	"JP-10": {Latitude: 36.3911, Longitude: 139.0608}, // Gunma
	"JP-11": {Latitude: 35.8569, Longitude: 139.6489}, // Saitama
	"JP-12": {Latitude: 35.6047, Longitude: 140.1233}, // Chiba
	"JP-13": {Latitude: 35.6894, Longitude: 139.6917}, // Tokyo
	"JP-14": {Latitude: 35.4478, Longitude: 139.6425}, // Kanagawa
	"JP-15": {Latitude: 37.9026, Longitude: 139.0236}, // Niigata
	"JP-16": {Latitude: 36.6953, Longitude: 137.2114}, // Toyama
	"JP-17": {Latitude: 36.5947, Longitude: 136.6256}, // Ishikawa
	"JP-18": {Latitude: 36.0652, Longitude: 136.2219}, // Fukui
	"JP-19": {Latitude: 35.6642, Longitude: 138.5683}, // Yamanashi
	"JP-20": {Latitude: 36.2333, Longitude: 138.1811}, // Nagano
	"JP-21": {Latitude: 35.3912, Longitude: 136.7222}, // Gifu
	"JP-22": {Latitude: 34.9769, Longitude: 138.3831}, // Shizuoka
	"JP-23": {Latitude: 35.1802, Longitude: 136.9066}, // Aichi
	"JP-24": {Latitude: 34.7303, Longitude: 136.5086}, // Mie
	"JP-25": {Latitude: 35.0045, Longitude: 135.8686}, // Shiga
	"JP-26": {Latitude: 35.0214, Longitude: 135.7556}, // Kyoto
	"JP-27": {Latitude: 34.6863, Longitude: 135.5200}, // Osaka
	"JP-28": {Latitude: 34.6913, Longitude: 135.1830}, // Hyogo
	"JP-29": {Latitude: 34.6853, Longitude: 135.8328}, // Nara
	"JP-30": {Latitude: 34.2260, Longitude: 135.1675}, // Wakayama
	"JP-31": {Latitude: 35.5039, Longitude: 134.2378}, // Tottori
	"JP-32": {Latitude: 35.4723, Longitude: 133.0505}, // Shimane
	"JP-33": {Latitude: 34.6618, Longitude: 133.9344}, // Okayama
	"JP-34": {Latitude: 34.3966, Longitude: 132.4596}, // Hiroshima
	"JP-35": {Latitude: 34.1861, Longitude: 131.4714}, // Yamaguchi
	"JP-36": {Latitude: 34.0658, Longitude: 134.5593}, // Tokushima
	"JP-37": {Latitude: 34.3401, Longitude: 134.0434}, // Kagawa
	"JP-38": {Latitude: 33.8416, Longitude: 132.7661}, // Ehime
	"JP-39": {Latitude: 33.5597, Longitude: 133.5311}, // Kochi
	"JP-40": {Latitude: 33.6064, Longitude: 130.4183}, // Fukuoka
	"JP-41": {Latitude: 33.2494, Longitude: 130.2988}, // Saga
	"JP-42": {Latitude: 32.7448, Longitude: 129.8737}, // Nagasaki
	"JP-43": {Latitude: 32.7898, Longitude: 130.7417}, // Kumamoto
	"JP-44": {Latitude: 33.2382, Longitude: 131.6126}, // Oita
	"JP-45": {Latitude: 31.9111, Longitude: 131.4239}, // Miyazaki
	"JP-46": {Latitude: 31.5602, Longitude: 130.5581}, // Kagoshima
	"JP-47": {Latitude: 26.3358, Longitude: 127.8011}, // Okinawa
}
