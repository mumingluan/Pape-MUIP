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

func (s *App) languageLookup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid text id"})
		return
	}
	text, err := s.localized(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"text_id": id, "text": text})
}

func (s *App) localized(id int64) (string, error) {
	var text string
	err := s.language.QueryRow("select text from localized_text where resource_set_id=? and text_id=?", s.cfg.LanguageSetID, id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text, err
}

func (s *App) localizeCatalog(data []byte) []byte {
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return data
	}
	rows, ok := payload["rows"].([]any)
	if !ok {
		return data
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id := number(row["name_text_id"])
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	names := map[int64]string{}
	if len(ids) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids)+1)
		args = append(args, s.cfg.LanguageSetID)
		for _, id := range ids {
			args = append(args, id)
		}
		query := "select text_id,text from localized_text where resource_set_id=? and text_id in (" + placeholders + ")"
		if result, err := s.language.Query(query, args...); err == nil {
			for result.Next() {
				var id int64
				var text string
				if result.Scan(&id, &text) == nil {
					names[id] = text
				}
			}
			result.Close()
		}
	}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id := number(row["name_text_id"])
		if names[id] != "" {
			row["localized_name"] = names[id]
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
