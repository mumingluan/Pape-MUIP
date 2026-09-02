package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func TestBuildFullCatalogGrantUsesOrdinaryResourceIDs(t *testing.T) {
	row := func(id string, typ int32, raw string) catalogRow {
		return catalogRow{ID: id, AssetType: typ, Raw: json.RawMessage(raw)}
	}
	catalogs := map[string]catalogResponse{
		"cards":    {Rows: []catalogRow{row("111101", 0, `{}`)}},
		"fashions": {Rows: []catalogRow{row("1001", 0, `{"IsEmpty":0}`), row("1002", 0, `{"IsEmpty":1}`)}},
		"items":    {Rows: []catalogRow{row("1", 1, `{}`), row("7000", 33, `{}`)}},
		"frames":   {Rows: []catalogRow{row("4000", 0, `{}`)}}, "titles": {Rows: []catalogRow{row("5000", 0, `{}`)}},
		"scenes": {Rows: []catalogRow{row("100", 0, `{}`)}}, "scene-places": {Rows: []catalogRow{row("1", 0, `{}`)}},
		"scores": {Rows: []catalogRow{row("1", 0, `{}`)}}, "weapons": {Rows: []catalogRow{row("70001", 0, `{}`)}},
		"weapon-skins": {Rows: []catalogRow{row("71010", 0, `{}`)}}, "collections": {Rows: []catalogRow{row("200", 0, `{}`)}},
		"decorations":   {Rows: []catalogRow{row("300", 0, `{}`)}},
		"player-levels": {Rows: []catalogRow{row("1", 0, `{"Level":1,"NextAddExp":100}`), row("2", 0, `{"Level":2,"NextAddExp":200}`)}},
		"love-levels":   {Rows: []catalogRow{row("1", 0, `{"Level":1,"NextAddLove":10}`), row("2", 0, `{"Level":2,"NextAddLove":20}`)}},
		"roles":         {Rows: []catalogRow{row("1", 0, `{}`)}},
	}
	grant, err := buildFullCatalogGrant(catalogs)
	if err != nil {
		t.Fatal(err)
	}
	if len(grant.CardIDs) != 1 || len(grant.FashionIDs) != 1 || grant.FashionIDs[0] != 1001 || len(grant.Assets) != 2 || grant.Assets[0].Quantity != 1_000_000 || len(grant.HeadIconIDs) != 1 || grant.PlayerLevel != 2 || grant.PlayerExp != 300 || len(grant.RoleLove) != 1 || grant.RoleLove[0].LoveLevel != 2 || grant.RoleLove[0].LovePoint != 19 {
		t.Fatalf("unexpected grant: %+v", grant)
	}
}

func TestParseRealSyncCaptureWhenConfigured(t *testing.T) {
	path := os.Getenv("PAPE_SYNC_CAPTURE_TEST_FILE")
	resourcePath := os.Getenv("PAPE_RESOURCE_TEST_DB")
	if path == "" || resourcePath == "" {
		t.Skip("PAPE_SYNC_CAPTURE_TEST_FILE and PAPE_RESOURCE_TEST_DB are not set")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var capture struct {
		Message struct {
			Name    string         `json:"name"`
			Decoded map[string]any `json:"decoded"`
		} `json:"message"`
	}
	if err := decoder.Decode(&capture); err != nil {
		t.Fatal(err)
	}
	if capture.Message.Name != "SyncUserTotalDataReply" {
		t.Fatalf("unexpected message %q", capture.Message.Name)
	}
	resourceDB, err := sql.Open("sqlite", resourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer resourceDB.Close()
	rows, err := resourceDB.Query(`select row_key,data_json from config_rows where table_name='Item'`)
	if err != nil {
		t.Fatal(err)
	}
	itemTypes := map[int32]int32{}
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			t.Fatal(err)
		}
		id, _ := strconv.ParseInt(key, 10, 32)
		itemTypes[int32(id)] = int32(rawInt(json.RawMessage(raw), "Type"))
	}
	rows.Close()
	state, _, err := parseSyncState(capture.Message.Decoded, itemTypes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if state.Profile == nil || len(state.Cards) == 0 || len(state.Stages) == 0 {
		t.Fatalf("real capture was not normalized: %+v", state)
	}
}

func TestParseSyncStateNormalizesCaptureWithoutForwardingSnapshot(t *testing.T) {
	root := map[string]any{
		"Base":     map[string]any{"Name": "名", "FamilyName": "姓", "Level": float64(2), "Exp": float64(5)},
		"Coin":     map[string]any{"Gold": float64(10)},
		"Item":     map[string]any{"Item": map[string]any{"ItemMap": map[string]any{"7000": map[string]any{"Id": float64(7000), "Type": float64(33), "Num": float64(1)}}}},
		"Card":     map[string]any{"CardMap": map[string]any{"111101": map[string]any{"Id": float64(111101), "Level": float64(10), "StarLevel": float64(2)}}},
		"Fashion":  map[string]any{"FashionMap": map[string]any{"1001": map[string]any{"Id": float64(1001)}}},
		"Frame":    map[string]any{"FrameMap": map[string]any{"4000": map[string]any{}}},
		"Title":    map[string]any{"TitleMap": map[string]any{"5000": map[string]any{}}},
		"Personal": map[string]any{"HeadIconMap": map[string]any{"7000": map[string]any{}}},
	}
	state, grants, err := parseSyncState(root, map[int32]int32{7000: 33}, []catalogRow{{Raw: json.RawMessage(`{"Level":1,"NextAddExp":100}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if state.Profile.Level != 2 || len(state.Cards) != 1 || len(state.Fashions) != 1 || len(state.Assets) < 2 || len(grants.FrameIDs) != 1 || len(grants.TitleIDs) != 1 || len(grants.HeadIconIDs) != 1 {
		t.Fatalf("unexpected normalized import: state=%+v grants=%+v", state, grants)
	}
}
