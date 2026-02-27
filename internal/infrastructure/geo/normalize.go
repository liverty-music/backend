// Package geo provides functions for normalizing and displaying
// ISO 3166-2 administrative area codes.
package geo

import "strings"

// NormalizeAdminArea converts a free-text administrative area string into
// the corresponding ISO 3166-2 subdivision code. It handles Japanese
// prefecture names (with or without suffix), English names, and
// case-insensitive matching.
//
// Returns nil when the input is empty, whitespace-only, or unrecognized.
// Callers should treat nil as "admin area unknown" and store NULL in the database.
func NormalizeAdminArea(text string) *string {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil
	}
	key := strings.ToLower(s)

	if code, ok := prefectureLookup[key]; ok {
		return &code
	}
	return nil
}

// prefectureLookup maps lowercase Japanese and English prefecture names to
// ISO 3166-2 codes. It includes variants with and without the administrative
// suffix (県/都/道/府).
var prefectureLookup = buildPrefectureLookup()

// prefectureEntry defines a single prefecture with its ISO code, Japanese
// names (with and without suffix), and English name.
type prefectureEntry struct {
	code     string
	jaFull   string // e.g., "北海道"
	jaShort  string // e.g., "北海道" (same for 道), "東京" (without 都)
	english  string // e.g., "hokkaido"
}

func buildPrefectureLookup() map[string]string {
	entries := []prefectureEntry{
		{"JP-01", "北海道", "北海道", "hokkaido"},
		{"JP-02", "青森県", "青森", "aomori"},
		{"JP-03", "岩手県", "岩手", "iwate"},
		{"JP-04", "宮城県", "宮城", "miyagi"},
		{"JP-05", "秋田県", "秋田", "akita"},
		{"JP-06", "山形県", "山形", "yamagata"},
		{"JP-07", "福島県", "福島", "fukushima"},
		{"JP-08", "茨城県", "茨城", "ibaraki"},
		{"JP-09", "栃木県", "栃木", "tochigi"},
		{"JP-10", "群馬県", "群馬", "gunma"},
		{"JP-11", "埼玉県", "埼玉", "saitama"},
		{"JP-12", "千葉県", "千葉", "chiba"},
		{"JP-13", "東京都", "東京", "tokyo"},
		{"JP-14", "神奈川県", "神奈川", "kanagawa"},
		{"JP-15", "新潟県", "新潟", "niigata"},
		{"JP-16", "富山県", "富山", "toyama"},
		{"JP-17", "石川県", "石川", "ishikawa"},
		{"JP-18", "福井県", "福井", "fukui"},
		{"JP-19", "山梨県", "山梨", "yamanashi"},
		{"JP-20", "長野県", "長野", "nagano"},
		{"JP-21", "岐阜県", "岐阜", "gifu"},
		{"JP-22", "静岡県", "静岡", "shizuoka"},
		{"JP-23", "愛知県", "愛知", "aichi"},
		{"JP-24", "三重県", "三重", "mie"},
		{"JP-25", "滋賀県", "滋賀", "shiga"},
		{"JP-26", "京都府", "京都", "kyoto"},
		{"JP-27", "大阪府", "大阪", "osaka"},
		{"JP-28", "兵庫県", "兵庫", "hyogo"},
		{"JP-29", "奈良県", "奈良", "nara"},
		{"JP-30", "和歌山県", "和歌山", "wakayama"},
		{"JP-31", "鳥取県", "鳥取", "tottori"},
		{"JP-32", "島根県", "島根", "shimane"},
		{"JP-33", "岡山県", "岡山", "okayama"},
		{"JP-34", "広島県", "広島", "hiroshima"},
		{"JP-35", "山口県", "山口", "yamaguchi"},
		{"JP-36", "徳島県", "徳島", "tokushima"},
		{"JP-37", "香川県", "香川", "kagawa"},
		{"JP-38", "愛媛県", "愛媛", "ehime"},
		{"JP-39", "高知県", "高知", "kochi"},
		{"JP-40", "福岡県", "福岡", "fukuoka"},
		{"JP-41", "佐賀県", "佐賀", "saga"},
		{"JP-42", "長崎県", "長崎", "nagasaki"},
		{"JP-43", "熊本県", "熊本", "kumamoto"},
		{"JP-44", "大分県", "大分", "oita"},
		{"JP-45", "宮崎県", "宮崎", "miyazaki"},
		{"JP-46", "鹿児島県", "鹿児島", "kagoshima"},
		{"JP-47", "沖縄県", "沖縄", "okinawa"},
	}

	m := make(map[string]string, len(entries)*3)
	for _, e := range entries {
		m[strings.ToLower(e.jaFull)] = e.code
		if e.jaFull != e.jaShort {
			m[strings.ToLower(e.jaShort)] = e.code
		}
		m[e.english] = e.code
	}
	return m
}
