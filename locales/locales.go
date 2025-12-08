package locales

import "strings"

var Supported = []string{"en", "zh"}

const Default = "en"

func IsSupported(locale string) bool {
	for _, candidate := range Supported {
		if strings.EqualFold(candidate, locale) {
			return true
		}
	}

	return false
}

func Normalize(locale string) string {
	for _, candidate := range Supported {
		if strings.EqualFold(candidate, locale) {
			return candidate
		}
	}

	return Default
}
