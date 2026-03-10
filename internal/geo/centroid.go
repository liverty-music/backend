package geo

// LatLng represents a WGS 84 geographic coordinate.
type LatLng struct {
	Lat float64
	Lng float64
}

// PrefectureCentroid returns the geographic centroid for a Japanese prefecture
// identified by its ISO 3166-2 code (e.g., "JP-13" for Tokyo).
// Returns the centroid and true if found, or zero value and false otherwise.
//
// Coordinates are approximate centroids sourced from the Geospatial Information
// Authority of Japan (GSI).
func PrefectureCentroid(code string) (LatLng, bool) {
	c, ok := prefectureCentroids[code]
	return c, ok
}

// prefectureCentroids maps ISO 3166-2 codes to approximate geographic centroids
// for all 47 Japanese prefectures.
var prefectureCentroids = map[string]LatLng{
	"JP-01": {Lat: 43.0642, Lng: 141.3469}, // Hokkaido
	"JP-02": {Lat: 40.8244, Lng: 140.7400}, // Aomori
	"JP-03": {Lat: 39.7036, Lng: 141.1527}, // Iwate
	"JP-04": {Lat: 38.2688, Lng: 140.8721}, // Miyagi
	"JP-05": {Lat: 39.7186, Lng: 140.1024}, // Akita
	"JP-06": {Lat: 38.2404, Lng: 140.3633}, // Yamagata
	"JP-07": {Lat: 37.7500, Lng: 140.4678}, // Fukushima
	"JP-08": {Lat: 36.3419, Lng: 140.4468}, // Ibaraki
	"JP-09": {Lat: 36.5658, Lng: 139.8836}, // Tochigi
	"JP-10": {Lat: 36.3911, Lng: 139.0608}, // Gunma
	"JP-11": {Lat: 35.8569, Lng: 139.6489}, // Saitama
	"JP-12": {Lat: 35.6047, Lng: 140.1233}, // Chiba
	"JP-13": {Lat: 35.6894, Lng: 139.6917}, // Tokyo
	"JP-14": {Lat: 35.4478, Lng: 139.6425}, // Kanagawa
	"JP-15": {Lat: 37.9026, Lng: 139.0236}, // Niigata
	"JP-16": {Lat: 36.6953, Lng: 137.2114}, // Toyama
	"JP-17": {Lat: 36.5947, Lng: 136.6256}, // Ishikawa
	"JP-18": {Lat: 36.0652, Lng: 136.2219}, // Fukui
	"JP-19": {Lat: 35.6642, Lng: 138.5683}, // Yamanashi
	"JP-20": {Lat: 36.2333, Lng: 138.1811}, // Nagano
	"JP-21": {Lat: 35.3912, Lng: 136.7222}, // Gifu
	"JP-22": {Lat: 34.9769, Lng: 138.3831}, // Shizuoka
	"JP-23": {Lat: 35.1802, Lng: 136.9066}, // Aichi
	"JP-24": {Lat: 34.7303, Lng: 136.5086}, // Mie
	"JP-25": {Lat: 35.0045, Lng: 135.8686}, // Shiga
	"JP-26": {Lat: 35.0214, Lng: 135.7556}, // Kyoto
	"JP-27": {Lat: 34.6863, Lng: 135.5200}, // Osaka
	"JP-28": {Lat: 34.6913, Lng: 135.1830}, // Hyogo
	"JP-29": {Lat: 34.6853, Lng: 135.8328}, // Nara
	"JP-30": {Lat: 34.2260, Lng: 135.1675}, // Wakayama
	"JP-31": {Lat: 35.5039, Lng: 134.2378}, // Tottori
	"JP-32": {Lat: 35.4723, Lng: 133.0505}, // Shimane
	"JP-33": {Lat: 34.6618, Lng: 133.9344}, // Okayama
	"JP-34": {Lat: 34.3966, Lng: 132.4596}, // Hiroshima
	"JP-35": {Lat: 34.1861, Lng: 131.4714}, // Yamaguchi
	"JP-36": {Lat: 34.0658, Lng: 134.5593}, // Tokushima
	"JP-37": {Lat: 34.3401, Lng: 134.0434}, // Kagawa
	"JP-38": {Lat: 33.8416, Lng: 132.7661}, // Ehime
	"JP-39": {Lat: 33.5597, Lng: 133.5311}, // Kochi
	"JP-40": {Lat: 33.6064, Lng: 130.4183}, // Fukuoka
	"JP-41": {Lat: 33.2494, Lng: 130.2988}, // Saga
	"JP-42": {Lat: 32.7448, Lng: 129.8737}, // Nagasaki
	"JP-43": {Lat: 32.7898, Lng: 130.7417}, // Kumamoto
	"JP-44": {Lat: 33.2382, Lng: 131.6126}, // Oita
	"JP-45": {Lat: 31.9111, Lng: 131.4239}, // Miyazaki
	"JP-46": {Lat: 31.5602, Lng: 130.5581}, // Kagoshima
	"JP-47": {Lat: 26.3358, Lng: 127.8011}, // Okinawa
}
