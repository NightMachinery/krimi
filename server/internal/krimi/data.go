package krimi

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed data/*.json
var embeddedGameData embed.FS

type localeData struct {
	Means    []string
	Clues    []string
	Analysis []AnalysisItem
}

type gameDataCatalog struct {
	locales map[string]localeData
}

func loadGameDataCatalog() (*gameDataCatalog, error) {
	catalog := &gameDataCatalog{locales: map[string]localeData{}}
	languages := []string{"en", "pt_br"}
	for _, lang := range languages {
		means, err := loadStringSlice(fmt.Sprintf("data/means.%s.json", lang))
		if err != nil {
			return nil, err
		}
		clues, err := loadStringSlice(fmt.Sprintf("data/clues.%s.json", lang))
		if err != nil {
			return nil, err
		}
		analysis, err := loadAnalysisSlice(fmt.Sprintf("data/analysis.%s.json", lang))
		if err != nil {
			return nil, err
		}
		catalog.locales[lang] = localeData{Means: means, Clues: clues, Analysis: analysis}
	}
	return catalog, nil
}

func (c *gameDataCatalog) Locale(lang string) localeData {
	if lang == "" {
		lang = "en"
	}
	if locale, ok := c.locales[lang]; ok {
		return locale
	}
	return c.locales["en"]
}

func loadStringSlice(path string) ([]string, error) {
	blob, err := embeddedGameData.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var values []string
	if err := json.Unmarshal(blob, &values); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return values, nil
}

func loadAnalysisSlice(path string) ([]AnalysisItem, error) {
	blob, err := embeddedGameData.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var values []AnalysisItem
	if err := json.Unmarshal(blob, &values); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return values, nil
}
