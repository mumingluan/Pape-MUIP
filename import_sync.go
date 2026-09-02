package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type importedProfile struct {
	Name        string `json:"name"`
	FamilyName  string `json:"family_name"`
	Level       int32  `json:"level"`
	LoginCount  int32  `json:"login_count"`
	CreatedAt   int64  `json:"created_at"`
	LastLoginAt int64  `json:"last_login_at"`
}
type importedCard struct {
	CardID     int32 `json:"card_id"`
	Level      int32 `json:"level"`
	Exp        int32 `json:"exp"`
	StarLevel  int32 `json:"star_level"`
	RankLevel  int32 `json:"rank_level"`
	Awaken     bool  `json:"awaken"`
	CreateTime int64 `json:"create_time"`
}
type importedTask struct {
	TaskID       int32 `json:"task_id"`
	Num          int64 `json:"num"`
	RewardCount  int32 `json:"reward_count"`
	CompleteTime int64 `json:"complete_time"`
	HasGotNext   bool  `json:"has_got_next"`
}
type importedStage struct {
	StageID    int32 `json:"stage_id"`
	CreateTime int64 `json:"create_time"`
	PassTime   int64 `json:"pass_time"`
	State      int32 `json:"state"`
	Reward     int32 `json:"reward"`
	Star       int32 `json:"star"`
	ShowStar   int32 `json:"show_star"`
}
type importedGuide struct {
	GroupID int32 `json:"group_id"`
	StepID  int32 `json:"step_id"`
	Status  int32 `json:"status"`
}
type importedUnlock struct {
	UnlockID int32 `json:"unlock_id"`
	IsRead   bool  `json:"is_read"`
}
type importedProgress struct {
	CurrentStageID        int32 `json:"current_stage_id"`
	MainlineChooseStageID int32 `json:"mainline_choose_stage_id"`
}
type importedState struct {
	Profile  *importedProfile  `json:"profile,omitempty"`
	Assets   []grantAsset      `json:"assets"`
	Cards    []importedCard    `json:"cards"`
	Fashions []int32           `json:"fashions"`
	Tasks    []importedTask    `json:"tasks"`
	Stages   []importedStage   `json:"stages"`
	Guides   []importedGuide   `json:"guides"`
	Unlocks  []importedUnlock  `json:"unlocks"`
	RoleLove []roleLoveState   `json:"role_love"`
	Progress *importedProgress `json:"progress,omitempty"`
}

func (s *Server) importSyncPlayer(c *gin.Context) {
	serverID, accountID, peer, ok := s.operationTarget(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<20)
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "选择一个抓包 JSON 文件"})
		return
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<20))
	decoder.UseNumber()
	var capture struct {
		Message struct {
			Name    string         `json:"name"`
			Decoded map[string]any `json:"decoded"`
		} `json:"message"`
	}
	if err := decoder.Decode(&capture); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if capture.Message.Name != "SyncUserTotalDataReply" || capture.Message.Decoded == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidSyncCapture.Error()})
		return
	}
	var itemCatalog, levelCatalog catalogResponse
	if err := s.callPeerJSON(c.Request.Context(), "booi:"+serverID, peer, http.MethodGet, "/inner/v1/admin/catalog/items?limit=20000&compact=true", nil, &itemCatalog); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if err := s.callPeerJSON(c.Request.Context(), "booi:"+serverID, peer, http.MethodGet, "/inner/v1/admin/catalog/player-levels?limit=20000", nil, &levelCatalog); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	itemTypes := map[int32]int32{}
	for _, row := range itemCatalog.Rows {
		if id, e := parsePositiveID(row.ID); e == nil {
			itemTypes[id] = row.AssetType
		}
	}
	state, grants, err := parseSyncState(capture.Message.Decoded, itemTypes, levelCatalog.Rows)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var stateResult map[string]any
	if err := s.callPeerJSON(c.Request.Context(), "booi:"+serverID, peer, http.MethodPut, fmt.Sprintf("/inner/v1/admin/players/%d/state", accountID), state, &stateResult); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	var grantResult map[string]any
	if err := s.callPeerJSON(c.Request.Context(), "booi:"+serverID, peer, http.MethodPut, fmt.Sprintf("/inner/v1/admin/players/%d/grants", accountID), grants, &grantResult); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "核心数据已导入，但所有权目录导入失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "resource_version": itemCatalog.ResourceVersion, "counts": gin.H{"assets": len(state.Assets), "cards": len(state.Cards), "fashions": len(state.Fashions), "tasks": len(state.Tasks), "stages": len(state.Stages), "guides": len(state.Guides), "unlocks": len(state.Unlocks), "role_love": len(state.RoleLove), "frames": len(grants.FrameIDs), "titles": len(grants.TitleIDs), "head_icons": len(grants.HeadIconIDs)}})
}

func parseSyncState(root map[string]any, itemTypes map[int32]int32, levels []catalogRow) (importedState, grantRequest, error) {
	state := importedState{}
	grants := grantRequest{}
	base := object(root["Base"])
	if base == nil {
		return state, grants, fmt.Errorf("SyncUserTotalDataReply has no Base")
	}
	state.Profile = &importedProfile{Name: textValue(base["Name"]), FamilyName: textValue(base["FamilyName"]), Level: int32(integer(base["Level"])), LoginCount: int32(integer(base["LoginNum"])), CreatedAt: integer(base["CreateTime"]), LastLoginAt: integer(base["LastLoginTime"])}
	if state.Profile.Level <= 0 {
		return state, grants, fmt.Errorf("invalid imported player level")
	}
	assets := map[[2]int32]int64{}
	coin := object(root["Coin"])
	for _, field := range []struct {
		name    string
		typ, id int32
	}{{"Gold", 1, 1}, {"Jewel", 2, 2}, {"Power", 3, 3}, {"StarJewel", 6, 6}, {"TestPoint", 7, 7}} {
		assets[[2]int32{field.typ, field.id}] = integer(coin[field.name])
	}
	totalExp := integer(base["Exp"])
	for _, row := range levels {
		if int32(rawInt(row.Raw, "Level")) < state.Profile.Level {
			totalExp += rawInt(row.Raw, "NextAddExp")
		}
	}
	assets[[2]int32{4, 4}] = totalExp
	for key, value := range objectPath(root, "Item", "Item", "ItemMap") {
		item := object(value)
		id := int32(integer(item["Id"]))
		if id == 0 {
			id = int32(parseMapKey(key))
		}
		typ := int32(integer(item["Type"]))
		if typ == 0 {
			typ = itemTypes[id]
		}
		if id <= 0 || typ <= 0 {
			return state, grants, fmt.Errorf("cannot infer imported item %s", key)
		}
		assets[[2]int32{typ, id}] = integer(item["Num"])
	}
	for key, value := range objectPath(root, "Item", "SpItem", "SpItemsMap") {
		id := int32(parseMapKey(key))
		var quantity int64
		for _, raw := range array(object(value)["SpItems"]) {
			quantity += integer(object(raw)["Num"])
		}
		if id > 0 {
			assets[[2]int32{40, id}] = quantity
		}
	}
	for key, quantity := range assets {
		if quantity < 0 {
			return state, grants, fmt.Errorf("negative imported asset %d:%d", key[0], key[1])
		}
		state.Assets = append(state.Assets, grantAsset{Type: key[0], ID: key[1], Quantity: quantity})
	}
	for key, value := range objectPath(root, "Card", "CardMap") {
		card := object(value)
		id := int32(integer(card["Id"]))
		if id == 0 {
			id = int32(parseMapKey(key))
		}
		if id > 0 {
			state.Cards = append(state.Cards, importedCard{CardID: id, Level: int32(integer(card["Level"])), Exp: int32(integer(card["Exp"])), StarLevel: int32(integer(card["StarLevel"])), RankLevel: int32(integer(card["PhaseLevel"])), Awaken: strings.EqualFold(textValue(card["Awaken"]), "Finish") || integer(card["Awaken"]) == 1, CreateTime: integer(card["CTime"])})
		}
	}
	for key, value := range objectPath(root, "Quest", "Quests") {
		quest := object(value)
		id := int32(integer(quest["ID"]))
		if id == 0 {
			id = int32(parseMapKey(key))
		}
		if id > 0 {
			state.Tasks = append(state.Tasks, importedTask{TaskID: id, Num: integer(quest["Num"]), RewardCount: int32(integer(quest["RewardCnt"])), CompleteTime: integer(quest["CompleteTm"]), HasGotNext: boolean(quest["HasGotNext"])})
		}
	}
	stageRoot := object(root["Stage"])
	for key, value := range object(stageRoot["StageMap"]) {
		stage := object(value)
		id := int32(integer(stage["StageID"]))
		if id == 0 {
			id = int32(parseMapKey(key))
		}
		if id > 0 {
			state.Stages = append(state.Stages, importedStage{StageID: id, CreateTime: integer(stage["CreateTime"]), PassTime: integer(stage["PassTime"]), State: int32(integer(stage["State"])), Reward: int32(integer(stage["Reward"])), Star: int32(integer(stage["Star"])), ShowStar: int32(integer(stage["ShowStar"]))})
		}
	}
	state.Progress = &importedProgress{CurrentStageID: int32(integer(stageRoot["CurStageID"])), MainlineChooseStageID: int32(integer(stageRoot["MainlineChooseStageID"]))}
	for key, value := range objectPath(root, "Guide", "UserGuideMap") {
		guide := object(value)
		id := int32(integer(guide["GroupID"]))
		if id == 0 {
			id = int32(parseMapKey(key))
		}
		if id > 0 {
			state.Guides = append(state.Guides, importedGuide{GroupID: id, StepID: int32(integer(guide["StepID"])), Status: int32(integer(guide["Status"]))})
		}
	}
	for key, value := range objectPath(root, "Unlock", "UnlockMap") {
		id := int32(parseMapKey(key))
		if id > 0 {
			state.Unlocks = append(state.Unlocks, importedUnlock{UnlockID: id, IsRead: boolean(value)})
		}
	}
	for key, value := range objectPath(root, "Role", "RoleMap") {
		role := object(value)
		id := int32(integer(role["Id"]))
		if id == 0 {
			id = int32(parseMapKey(key))
		}
		if id > 0 {
			state.RoleLove = append(state.RoleLove, roleLoveState{RoleID: id, LovePoint: integer(role["LovePoint"]), LoveLevel: int32(integer(role["LoveLevel"]))})
		}
	}
	for key, value := range objectPath(root, "Fashion", "FashionMap") {
		fashion := object(value)
		id := int32(integer(fashion["Id"]))
		if id == 0 {
			id = int32(parseMapKey(key))
		}
		if id > 0 {
			state.Fashions = append(state.Fashions, id)
		}
	}
	grants.FrameIDs = mapIDs(objectPath(root, "Frame", "FrameMap"))
	grants.SelectedFrameID = int32(integer(object(root["Frame"])["FrameID"]))
	grants.TitleIDs = mapIDs(objectPath(root, "Title", "TitleMap"))
	title := object(root["Title"])
	if title != nil {
		grants.SelectedTitle = &selectedTitle{Prefix: int32(integer(title["TitlePrefix"])), Suffix: int32(integer(title["TitleSuffix"])), Background: int32(integer(title["TitleBg"])), Standalone: int32(integer(title["StandaloneTitle"]))}
	}
	grants.HeadIconIDs = mapIDs(objectPath(root, "Personal", "HeadIconMap"))
	head := object(object(root["Personal"])["Head"])
	if head != nil {
		grants.SelectedHead = &selectedHead{Type: int32(integer(head["Type"])), CardID: int32(integer(head["CardId"])), DollID: int32(integer(head["DollID"])), MiaoCardID: int32(integer(head["MiaoCardID"])), HeadIconID: int32(integer(head["HeadIconID"])), LastSetTime: integer(head["LastSetTime"])}
	}
	grants.SceneIDs = mapIDs(objectPath(root, "Scene", "SceneMap"))
	grants.ScenePlaceIDs = mapIDs(objectPath(root, "Scene", "PlaceMap"))
	grants.CurrentScenePlaceID = int32(integer(object(root["Scene"])["CurScenePlaceID"]))
	grants.ScoreIDs = mapIDs(objectPath(root, "SCore", "SCoreMap"))
	grants.WeaponIDs = mapIDs(objectPath(root, "Weapon", "WeaponMap"))
	grants.WeaponSkinIDs = mapIDs(objectPath(root, "Weapon", "WeaponFashionMap"))
	grants.DecorationIDs = mapIDs(objectPath(root, "Decoration", "DecorationInfoMap"))
	grants.CollectionIDs = append(grants.CollectionIDs, mapIDs(objectPath(root, "Collection", "NormalCollectionData", "CollectionInfoMap"))...)
	for _, value := range objectPath(root, "Collection", "RoleCollectionMap") {
		grants.CollectionIDs = append(grants.CollectionIDs, mapIDs(object(object(value)["CollectionMap"]))...)
	}
	return state, grants, nil
}

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func array(value any) []any           { result, _ := value.([]any); return result }
func objectPath(root map[string]any, path ...string) map[string]any {
	var current any = root
	for _, key := range path {
		current = object(current)[key]
	}
	return object(current)
}
func parseMapKey(value string) int64 { result, _ := strconv.ParseInt(value, 10, 64); return result }
func textValue(value any) string {
	if result, ok := value.(string); ok {
		return result
	}
	return ""
}
func mapIDs(values map[string]any) []int32 {
	result := make([]int32, 0, len(values))
	for key := range values {
		if id := parseMapKey(key); id > 0 && id <= 1<<31-1 {
			result = append(result, int32(id))
		}
	}
	return result
}
