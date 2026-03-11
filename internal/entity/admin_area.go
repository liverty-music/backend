package entity

// AdminAreaResolver converts ISO 3166-2 subdivision codes to human-readable names.
// Implementations hold the localized display-name lookup tables.
type AdminAreaResolver interface {
	// DisplayName returns the human-readable name for the given ISO 3166-2 code
	// in the requested language (e.g., "en", "ja"). Returns the code unchanged
	// if the code is unknown or the language is unsupported.
	DisplayName(code, lang string) string
}
