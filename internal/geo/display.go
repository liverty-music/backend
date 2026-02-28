package geo

// DisplayName converts an ISO 3166-2 code to a human-readable name in the
// requested language. It is used to produce search-friendly text when
// querying external services (MusicBrainz, Google Maps) with a venue's
// admin_area code.
//
// Supported lang values: "ja" (Japanese with suffix), "en" (English).
// Returns the code unchanged if the code is unknown or the language is
// unsupported.
func DisplayName(code, lang string) string {
	names, ok := displayNames[code]
	if !ok {
		return code
	}
	switch lang {
	case "ja":
		return names.ja
	case "en":
		return names.en
	default:
		return names.en
	}
}

type localizedName struct {
	ja string
	en string
}

var displayNames = buildDisplayNames()

func buildDisplayNames() map[string]localizedName {
	type entry struct {
		code string
		ja   string
		en   string
	}
	entries := []entry{
		{"JP-01", "北海道", "Hokkaido"},
		{"JP-02", "青森県", "Aomori"},
		{"JP-03", "岩手県", "Iwate"},
		{"JP-04", "宮城県", "Miyagi"},
		{"JP-05", "秋田県", "Akita"},
		{"JP-06", "山形県", "Yamagata"},
		{"JP-07", "福島県", "Fukushima"},
		{"JP-08", "茨城県", "Ibaraki"},
		{"JP-09", "栃木県", "Tochigi"},
		{"JP-10", "群馬県", "Gunma"},
		{"JP-11", "埼玉県", "Saitama"},
		{"JP-12", "千葉県", "Chiba"},
		{"JP-13", "東京都", "Tokyo"},
		{"JP-14", "神奈川県", "Kanagawa"},
		{"JP-15", "新潟県", "Niigata"},
		{"JP-16", "富山県", "Toyama"},
		{"JP-17", "石川県", "Ishikawa"},
		{"JP-18", "福井県", "Fukui"},
		{"JP-19", "山梨県", "Yamanashi"},
		{"JP-20", "長野県", "Nagano"},
		{"JP-21", "岐阜県", "Gifu"},
		{"JP-22", "静岡県", "Shizuoka"},
		{"JP-23", "愛知県", "Aichi"},
		{"JP-24", "三重県", "Mie"},
		{"JP-25", "滋賀県", "Shiga"},
		{"JP-26", "京都府", "Kyoto"},
		{"JP-27", "大阪府", "Osaka"},
		{"JP-28", "兵庫県", "Hyogo"},
		{"JP-29", "奈良県", "Nara"},
		{"JP-30", "和歌山県", "Wakayama"},
		{"JP-31", "鳥取県", "Tottori"},
		{"JP-32", "島根県", "Shimane"},
		{"JP-33", "岡山県", "Okayama"},
		{"JP-34", "広島県", "Hiroshima"},
		{"JP-35", "山口県", "Yamaguchi"},
		{"JP-36", "徳島県", "Tokushima"},
		{"JP-37", "香川県", "Kagawa"},
		{"JP-38", "愛媛県", "Ehime"},
		{"JP-39", "高知県", "Kochi"},
		{"JP-40", "福岡県", "Fukuoka"},
		{"JP-41", "佐賀県", "Saga"},
		{"JP-42", "長崎県", "Nagasaki"},
		{"JP-43", "熊本県", "Kumamoto"},
		{"JP-44", "大分県", "Oita"},
		{"JP-45", "宮崎県", "Miyazaki"},
		{"JP-46", "鹿児島県", "Kagoshima"},
		{"JP-47", "沖縄県", "Okinawa"},
	}

	m := make(map[string]localizedName, len(entries))
	for _, e := range entries {
		m[e.code] = localizedName{ja: e.ja, en: e.en}
	}
	return m
}
