package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var reportReasonTextIDs = map[int64]int64{1: 567402, 2: 567403, 3: 549608, 4: 549609, 5: 549610, 6: 309322, 7: 567404, 8: 549613, 9: 549614, 10: 549615}
var reportSourceNames = map[int64]string{101: "玩家资料页"}
var reportPlatformNames = map[int64]string{1: "iOS", 2: "Android"}

type languageOption struct {
	ResourceSetID int64  `json:"resource_set_id"`
	LanguageKey   string `json:"language_key"`
	Locale        string `json:"locale"`
	Name          string `json:"name"`
	TextCount     int64  `json:"text_count"`
	Supported     bool   `json:"supported"`
}

func (s *App) languages(c *gin.Context) {
	rows, err := s.languageOptions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"languages": rows, "selected_resource_set_id": s.languageSetID(c)})
}

func (s *App) languageOptions() ([]languageOption, error) {
	defaultID := s.defaultLanguageSetID()
	rows, err := s.language.Query(`select resource_set_id, language_key, text_count from language_resource_sets order by resource_set_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []languageOption{}
	for rows.Next() {
		var item languageOption
		if err := rows.Scan(&item.ResourceSetID, &item.LanguageKey, &item.TextCount); err != nil {
			return nil, err
		}
		item.Locale, item.Name = languageLabel(item.ResourceSetID, item.LanguageKey, defaultID)
		item.Supported = item.TextCount > 0
		result = append(result, item)
	}
	return result, rows.Err()
}

func languageLabel(id int64, key string, defaultID int64) (string, string) {
	if id == defaultID {
		return "zh-CN", "简体中文"
	}
	return "resource-" + key, "语言资源集 " + key
}

func (s *App) languageSetID(c *gin.Context) int64 {
	defaultID := s.defaultLanguageSetID()
	for _, raw := range []string{c.Query("language_set_id"), c.GetHeader("X-MUIP-Language-Set")} {
		if id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && id > 0 && s.languageSetExists(id) {
			return id
		}
	}
	if strings.HasPrefix(strings.ToLower(c.GetHeader("Accept-Language")), "zh") && defaultID != 0 {
		return defaultID
	}
	return defaultID
}

func (s *App) defaultLanguageSetID() int64 {
	var id int64
	if err := s.language.QueryRow("select resource_set_id from language_resource_sets where text_count > 0 order by resource_set_id limit 1").Scan(&id); err == nil && id != 0 {
		return id
	}
	// Older or test databases may only contain localized_text. Derive the
	// populated set there so localization remains compatible without a fixed ID.
	if err := s.language.QueryRow("select resource_set_id from localized_text group by resource_set_id order by count(*) desc, resource_set_id limit 1").Scan(&id); err == nil && id != 0 {
		return id
	}
	if id != 0 {
		return id
	}
	_ = s.language.QueryRow("select resource_set_id from language_resource_sets order by resource_set_id limit 1").Scan(&id)
	return id
}

func (s *App) languageSetExists(id int64) bool {
	var found int
	if s.language.QueryRow("select 1 from language_resource_sets where resource_set_id=? and text_count > 0", id).Scan(&found) == nil {
		return true
	}
	return s.language.QueryRow("select 1 from localized_text where resource_set_id=? limit 1", id).Scan(&found) == nil
}

func (s *App) languageLookup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid text id"})
		return
	}
	text, err := s.localizedIn(s.languageSetID(c), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"text_id": id, "text": text})
}

func (s *App) localized(id int64) (string, error) { return s.localizedIn(s.defaultLanguageSetID(), id) }

func (s *App) localizedIn(setID, id int64) (string, error) {
	var text string
	err := s.language.QueryRow("select text from localized_text where resource_set_id=? and text_id=?", setID, id).Scan(&text)
	defaultID := s.defaultLanguageSetID()
	if errors.Is(err, sql.ErrNoRows) && setID != defaultID {
		err = s.language.QueryRow("select text from localized_text where resource_set_id=? and text_id=?", defaultID, id).Scan(&text)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text, err
}

func (s *App) localizeCatalog(data []byte) []byte { return s.localizeCatalogIn(data, s.defaultLanguageSetID()) }

func (s *App) localizeCatalogIn(data []byte, setID int64) []byte {
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return data
	}
	rows, ok := payload["rows"].([]any)
	if !ok {
		return data
	}
	ids, seen := []int64{}, map[int64]bool{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id := number(row["name_text_id"])
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	names := s.localizedBatch(setID, ids)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id := number(row["name_text_id"])
		if names[id] != "" {
			row["localized_name"] = stripRichText(names[id])
		} else if fallback, _ := row["fallback_name"].(string); fallback != "" {
			row["localized_name"] = fallback
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return data
	}
	return encoded
}

func (s *App) localizeMailTemplates(data []byte, setID int64) []byte {
	var payload struct {
		Templates []map[string]any `json:"templates"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return data
	}
	ids, seen := []int64{}, map[int64]bool{}
	for _, row := range payload.Templates {
		for _, key := range []string{"title_text_id", "content_text_id", "sign_text_id"} {
			id := number(row[key])
			if id > 0 && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	texts := s.localizedBatch(setID, ids)
	for _, row := range payload.Templates {
		row["title"] = stripRichText(texts[number(row["title_text_id"])])
		row["content"] = stripRichText(texts[number(row["content_text_id"])])
		row["sign"] = stripRichText(texts[number(row["sign_text_id"])])
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return data
	}
	return encoded
}

func (s *App) localizedBatch(setID int64, ids []int64) map[int64]string {
	result := map[int64]string{}
	if len(ids) == 0 {
		return result
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, setID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.language.Query("select text_id,text from localized_text where resource_set_id=? and text_id in ("+placeholders+")", args...)
	if err == nil {
		for rows.Next() {
			var id int64
			var text string
			if rows.Scan(&id, &text) == nil {
				result[id] = text
			}
		}
		rows.Close()
	}
	defaultID := s.defaultLanguageSetID()
	if setID != defaultID {
		missing := []int64{}
		for _, id := range ids {
			if result[id] == "" {
				missing = append(missing, id)
			}
		}
		for id, text := range s.localizedBatch(defaultID, missing) {
			result[id] = text
		}
	}
	return result
}

func (s *App) localizeReports(data []byte) []byte { return s.localizeReportsIn(data, s.defaultLanguageSetID()) }

func (s *App) localizeReportsIn(data []byte, setID int64) []byte {
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return data
	}
	reports, ok := payload["reports"].([]any)
	if !ok {
		return data
	}
	ids := make([]int64, 0, len(reportReasonTextIDs))
	for _, id := range reportReasonTextIDs {
		ids = append(ids, id)
	}
	texts := s.localizedBatch(setID, ids)
	localized := map[int64]string{}
	for reasonID, textID := range reportReasonTextIDs {
		localized[reasonID] = stripRichText(texts[textID])
	}
	for _, raw := range reports {
		report, _ := raw.(map[string]any)
		if name := localized[number(report["reason_id"])]; name != "" {
			report["reason_name"] = name
		}
		if name := reportSourceNames[number(report["source"])]; name != "" {
			report["source_name"] = name
		}
		if name := reportPlatformNames[number(report["platform"])]; name != "" {
			report["platform_name"] = name
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return data
	}
	return encoded
}

func stripRichText(value string) string {
	var result strings.Builder
	inTag := false
	for _, char := range value {
		switch char {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				result.WriteRune(char)
			}
		}
	}
	return result.String()
}

func number(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}
