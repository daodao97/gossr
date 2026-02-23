package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daodao97/gossr/locales"
)

const localeSourceDir = "src/locales"

type LocaleMessages struct {
	messages      map[string]map[string]string
	defaultLocale string
}

func LoadLocaleMessages() (*LocaleMessages, error) {
	entries, err := fs.ReadDir(Locales, localeSourceDir)
	if err != nil {
		return nil, fmt.Errorf("read embedded locales dir: %w", err)
	}

	messages := make(map[string]map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fileName := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(strings.ToLower(fileName), ".json") {
			continue
		}

		locale := normalizeLocaleCode(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
		if locale == "" {
			continue
		}

		raw, readErr := fs.ReadFile(Locales, filepath.Join(localeSourceDir, fileName))
		if readErr != nil {
			return nil, fmt.Errorf("read embedded locale file %q: %w", fileName, readErr)
		}

		dict := make(map[string]string)
		if unmarshalErr := json.Unmarshal(raw, &dict); unmarshalErr != nil {
			return nil, fmt.Errorf("decode locale file %q: %w", fileName, unmarshalErr)
		}
		messages[locale] = dict
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("no locale json found in %s", localeSourceDir)
	}

	defaultLocale := locales.Default
	if _, ok := messages[defaultLocale]; !ok {
		defaultLocale = pickFirstLocale(messages)
	}

	return &LocaleMessages{
		messages:      messages,
		defaultLocale: defaultLocale,
	}, nil
}

func (m *LocaleMessages) Translate(locale string, key string) string {
	if m == nil || key == "" {
		return key
	}

	normalizedLocale := normalizeLocaleCode(locale)
	if message, ok := m.lookup(normalizedLocale, key); ok {
		return message
	}

	// 与前端 i18n 一致：当前语言缺失时回退到默认语言，再回退 key。
	if message, ok := m.lookup(m.defaultLocale, key); ok {
		return message
	}

	return key
}

func (m *LocaleMessages) lookup(locale string, key string) (string, bool) {
	if locale == "" {
		return "", false
	}
	dict, ok := m.messages[locale]
	if !ok {
		return "", false
	}
	message, ok := dict[key]
	return message, ok
}

func normalizeLocaleCode(raw string) string {
	candidate := strings.TrimSpace(strings.ToLower(raw))
	if candidate == "" {
		return ""
	}
	return candidate
}

func pickFirstLocale(messages map[string]map[string]string) string {
	candidates := make([]string, 0, len(messages))
	for locale := range messages {
		candidates = append(candidates, locale)
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return locales.Default
	}
	return candidates[0]
}
