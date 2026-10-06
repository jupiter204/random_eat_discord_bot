package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"random_eat_discord/internal/googlemaps"
	"random_eat_discord/internal/lottery"
)

func TestRenderDrawEmbed_PrivacyAndComponents(t *testing.T) {
	res := &lottery.DrawResult{
		SessionID: "test-sess",
		Place: &googlemaps.Place{
			ID:               "p1",
			DisplayName:      googlemaps.LocalizedText{Text: "美味餐廳"},
			FormattedAddress: "台北市信義區信義路五段7號",
			Rating:           4.6,
			UserRatingCount:  100,
			PriceLevel:       "PRICE_LEVEL_MODERATE",
		},
		RemainingCount: 2,
		QueryCtx: lottery.QueryContext{
			Radius:       1000,
			LocationName: "預設位置",
			Initiator:    "TestUser",
		},
	}

	embed, components := RenderDrawEmbed(res)

	// 驗證標題與價位
	if embed.Title != "🍽️ 美味餐廳" {
		t.Errorf("unexpected embed title: %s", embed.Title)
	}

	// 驗證搜尋條件欄位未洩漏私人名稱
	for _, field := range embed.Fields {
		if field.Name == "🎯 搜尋條件" {
			if strings.Contains(field.Value, "我家") || strings.Contains(field.Value, "公司") {
				t.Errorf("private name found in search condition: %s", field.Value)
			}
			if !strings.Contains(field.Value, "預設位置") {
				t.Errorf("expected 預設位置 in field value: %s", field.Value)
			}
		}
	}

	// 剩餘 2 家時，按鈕應為啟用
	if len(components) == 0 {
		t.Fatalf("expected components")
	}
	actionsRow := components[0].(discordgo.ActionsRow)
	btn := actionsRow.Components[0].(discordgo.Button)
	if btn.Disabled {
		t.Errorf("expected button to be enabled when remaining > 0")
	}

	// 測試剩餘 0 家時按鈕應被停用
	resExhausted := &lottery.DrawResult{
		SessionID:      "test-sess",
		Place:          res.Place,
		RemainingCount: 0,
		QueryCtx:       res.QueryCtx,
	}

	_, componentsExhausted := RenderDrawEmbed(resExhausted)
	actionsRowExhausted := componentsExhausted[0].(discordgo.ActionsRow)
	btnExhausted := actionsRowExhausted.Components[0].(discordgo.Button)
	if !btnExhausted.Disabled {
		t.Errorf("expected button to be disabled when remaining == 0")
	}
	if btnExhausted.Label != "已無更多備選餐廳" {
		t.Errorf("expected button label '已無更多備選餐廳', got %s", btnExhausted.Label)
	}
}
