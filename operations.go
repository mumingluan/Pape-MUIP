package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type catalogRow struct {
	ID        string          `json:"id"`
	AssetType int32           `json:"asset_type"`
	Raw       json.RawMessage `json:"raw"`
}

type catalogResponse struct {
	ResourceVersion string       `json:"resource_version"`
	Rows            []catalogRow `json:"rows"`
}

type grantAsset struct {
	Type     int32 `json:"type"`
	ID       int32 `json:"id"`
	Quantity int64 `json:"quantity"`
}

type roleLoveState struct {
	RoleID    int32 `json:"role_id"`
	LovePoint int64 `json:"love_point"`
	LoveLevel int32 `json:"love_level"`
}

type grantRequest struct {
	Assets              []grantAsset    `json:"assets"`
	CardIDs             []int32         `json:"card_ids"`
	FashionIDs          []int32         `json:"fashion_ids"`
	HeadIconIDs         []int32         `json:"head_icon_ids"`
	FrameIDs            []int32         `json:"frame_ids"`
	TitleIDs            []int32         `json:"title_ids"`
	SceneIDs            []int32         `json:"scene_ids"`
	ScenePlaceIDs       []int32         `json:"scene_place_ids"`
	ScoreIDs            []int32         `json:"score_ids"`
	WeaponIDs           []int32         `json:"weapon_ids"`
	WeaponSkinIDs       []int32         `json:"weapon_skin_ids"`
	CollectionIDs       []int32         `json:"collection_ids"`
	DecorationIDs       []int32         `json:"decoration_ids"`
	RoleLove            []roleLoveState `json:"role_love"`
	CardLevel           int32           `json:"card_level"`
	CardStarLevel       int32           `json:"card_star_level"`
	CardRankLevel       int32           `json:"card_rank_level"`
	CardAwaken          bool            `json:"card_awaken"`
	PlayerLevel         int32           `json:"player_level"`
	PlayerExp           int64           `json:"player_exp"`
	SelectedFrameID     int32           `json:"selected_frame_id,omitempty"`
	SelectedHead        *selectedHead   `json:"selected_head,omitempty"`
	SelectedTitle       *selectedTitle  `json:"selected_title,omitempty"`
	CurrentScenePlaceID int32           `json:"current_scene_place_id,omitempty"`
}

type selectedHead struct {
	Type        int32 `json:"type"`
	CardID      int32 `json:"card_id"`
	DollID      int32 `json:"doll_id"`
	MiaoCardID  int32 `json:"miao_card_id"`
	HeadIconID  int32 `json:"head_icon_id"`
	LastSetTime int64 `json:"last_set_time"`
}
type selectedTitle struct {
	Prefix     int32 `json:"prefix"`
	Suffix     int32 `json:"suffix"`
	Background int32 `json:"background"`
	Standalone int32 `json:"standalone"`
}

func (s *Server) fullCatalogPlayer(c *gin.Context) {
	serverID, accountID, peer, ok := s.operationTarget(c)
	if !ok {
		return
	}
	kinds := []string{"cards", "fashions", "items", "frames", "titles", "scenes", "scene-places", "scores", "weapons", "weapon-skins", "collections", "decorations", "player-levels", "love-levels", "roles"}
	catalogs := make(map[string]catalogResponse, len(kinds))
	for _, kind := range kinds {
		compact := kind != "fashions" && kind != "player-levels" && kind != "love-levels"
		path := "/inner/v1/admin/catalog/" + kind + "?limit=20000"
		if compact {
			path += "&compact=true"
		}
		var result catalogResponse
		if err := s.callPeerJSON(c.Request.Context(), "booi:"+serverID, peer, http.MethodGet, path, nil, &result); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("load %s catalog: %v", kind, err)})
			return
		}
		catalogs[kind] = result
	}
	grant, err := buildFullCatalogGrant(catalogs)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	var result map[string]any
	if err := s.callPeerJSON(c.Request.Context(), "booi:"+serverID, peer, http.MethodPut,
		fmt.Sprintf("/inner/v1/admin/players/%d/grants", accountID), grant, &result); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	result["resource_version"] = catalogs["items"].ResourceVersion
	c.JSON(http.StatusOK, result)
}

func buildFullCatalogGrant(catalogs map[string]catalogResponse) (grantRequest, error) {
	grant := grantRequest{CardLevel: 80, CardStarLevel: 8, CardRankLevel: 3, CardAwaken: true}
	var err error
	if grant.CardIDs, err = catalogIDs(catalogs["cards"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.FashionIDs, err = catalogIDs(catalogs["fashions"].Rows, func(row catalogRow) bool { return rawInt(row.Raw, "IsEmpty") == 0 }); err != nil {
		return grant, err
	}
	if grant.FrameIDs, err = catalogIDs(catalogs["frames"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.TitleIDs, err = catalogIDs(catalogs["titles"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.SceneIDs, err = catalogIDs(catalogs["scenes"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.ScenePlaceIDs, err = catalogIDs(catalogs["scene-places"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.ScoreIDs, err = catalogIDs(catalogs["scores"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.WeaponIDs, err = catalogIDs(catalogs["weapons"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.WeaponSkinIDs, err = catalogIDs(catalogs["weapon-skins"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.CollectionIDs, err = catalogIDs(catalogs["collections"].Rows, nil); err != nil {
		return grant, err
	}
	if grant.DecorationIDs, err = catalogIDs(catalogs["decorations"].Rows, nil); err != nil {
		return grant, err
	}
	currencyTypes := map[int32]bool{1: true, 2: true, 3: true, 4: true, 6: true, 7: true}
	for _, row := range catalogs["items"].Rows {
		id, parseErr := parsePositiveID(row.ID)
		if parseErr != nil {
			return grant, fmt.Errorf("item catalog: %w", parseErr)
		}
		quantity := int64(1)
		if currencyTypes[row.AssetType] {
			quantity = 1_000_000
		}
		grant.Assets = append(grant.Assets, grantAsset{Type: row.AssetType, ID: id, Quantity: quantity})
		if row.AssetType == 33 {
			grant.HeadIconIDs = append(grant.HeadIconIDs, id)
		}
	}
	for _, row := range catalogs["player-levels"].Rows {
		level := int32(rawInt(row.Raw, "Level"))
		if level > grant.PlayerLevel {
			grant.PlayerLevel = level
		}
		if next := rawInt(row.Raw, "NextAddExp"); next > 0 {
			grant.PlayerExp += next
		}
	}
	maxLoveLevel := int32(1)
	maxLovePoint := int64(0)
	for _, row := range catalogs["love-levels"].Rows {
		level := int32(rawInt(row.Raw, "Level"))
		if level >= maxLoveLevel {
			maxLoveLevel = level
			if next := rawInt(row.Raw, "NextAddLove"); next > 0 {
				maxLovePoint = next - 1
			}
		}
	}
	roleIDs, err := catalogIDs(catalogs["roles"].Rows, nil)
	if err != nil {
		return grant, err
	}
	for _, id := range roleIDs {
		grant.RoleLove = append(grant.RoleLove, roleLoveState{RoleID: id, LoveLevel: maxLoveLevel, LovePoint: maxLovePoint})
	}
	return grant, nil
}

func catalogIDs(rows []catalogRow, keep func(catalogRow) bool) ([]int32, error) {
	ids := make([]int32, 0, len(rows))
	for _, row := range rows {
		if keep != nil && !keep(row) {
			continue
		}
		id, err := parsePositiveID(row.ID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func parsePositiveID(value string) (int32, error) {
	id, err := strconv.ParseInt(value, 10, 32)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid resource ID %q", value)
	}
	return int32(id), nil
}

func rawInt(raw json.RawMessage, field string) int64 {
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return 0
	}
	return integer(values[field])
}

func integer(value any) int64 {
	switch value := value.(type) {
	case float64:
		return int64(value)
	case json.Number:
		result, _ := value.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(value, 10, 64)
		return result
	case bool:
		if value {
			return 1
		}
	}
	return 0
}

func boolean(value any) bool {
	if result, ok := value.(bool); ok {
		return result
	}
	return integer(value) != 0
}

func (s *Server) operationTarget(c *gin.Context) (string, uint64, Peer, bool) {
	serverID := c.Param("server")
	peer, ok := s.cfg.BOOI[serverID]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown BOOI server"})
		return "", 0, Peer{}, false
	}
	accountID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return "", 0, Peer{}, false
	}
	return serverID, accountID, peer, true
}

func (s *Server) callPeerJSON(ctx context.Context, key string, peer Peer, method, path string, input, output any) error {
	base, err := url.Parse(strings.TrimRight(peer.BaseURL, "/"))
	if err != nil {
		return err
	}
	reference, err := url.Parse(path)
	if err != nil {
		return err
	}
	target := base.ResolveReference(reference)
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+peer.AuthToken)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	s.clientsMu.Lock()
	client := s.clients[key]
	if client == nil {
		timeout := peer.TimeoutSeconds
		if timeout <= 0 {
			timeout = 30
		}
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second}
		s.clients[key] = client
	}
	s.clientsMu.Unlock()
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &problem)
		if problem.Error == "" {
			problem.Error = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("BOOI returned %s: %s", response.Status, problem.Error)
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return err
		}
	}
	return nil
}

var errInvalidSyncCapture = errors.New("file is not a decoded SyncUserTotalDataReply capture")
