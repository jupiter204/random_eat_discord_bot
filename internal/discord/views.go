package discord

import (
	"fmt"

	"random_eat_discord/internal/db"
	"random_eat_discord/internal/lottery"

	"github.com/bwmarrin/discordgo"
)

const (
	colorSuccess = 0xF59E0B // 活力橘黃
	colorInfo    = 0x3B82F6 // 科技藍
	colorError   = 0xEF4444 // 警示紅
)

// RenderDrawEmbed 產生抽籤結果的 Rich Embed 與 Reroll 按鈕元件
func RenderDrawEmbed(res *lottery.DrawResult) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	place := res.Place

	var ratingText string
	if place.Rating > 0 {
		ratingText = fmt.Sprintf("⭐ %.1f (%d 則評論)", place.Rating, place.UserRatingCount)
	} else {
		ratingText = "⭐ 暫無評分"
	}

	description := fmt.Sprintf("%s • 價位：%s • %s", ratingText, place.FormattedPriceLevel(), place.OpenStatusText())

	fields := []*discordgo.MessageEmbedField{
		{
			Name:   "📍 地址",
			Value:  place.FormattedAddress,
			Inline: false,
		},
		{
			Name:   "⏰ 營業時間",
			Value:  place.TodayOpeningHours(),
			Inline: false,
		},
		{
			Name: "🎯 搜尋條件",
			Value: fmt.Sprintf("中心：%s\n半徑：%d 公尺%s",
				res.QueryCtx.LocationName,
				res.QueryCtx.Radius,
				func() string {
					if res.QueryCtx.Keyword != "" {
						return fmt.Sprintf("\n關鍵字：%s", res.QueryCtx.Keyword)
					}
					return ""
				}(),
			),
			Inline: false,
		},
	}

	footerText := fmt.Sprintf("剩餘 %d 家備選餐廳", res.RemainingCount)
	if res.QueryCtx.Initiator != "" {
		footerText = fmt.Sprintf("由 %s 發起 • %s", res.QueryCtx.Initiator, footerText)
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("🍽️ %s", place.DisplayName.Text),
		URL:         place.GoogleMapsURI,
		Description: description,
		Color:       colorSuccess,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: footerText,
		},
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "🔄 再抽一家 (不消耗 API)",
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("reroll:%s", res.SessionID),
				},
			},
		},
	}

	return embed, components
}

// RenderPreferenceEmbed 產生預設偏好設定成功的 Embed
func RenderPreferenceEmbed(pref *db.UserPreference) *discordgo.MessageEmbed {
	name := pref.LocationName
	if name == "" {
		name = "（未命名）"
	}

	return &discordgo.MessageEmbed{
		Title:       "✅ 預設搜尋坐標已成功儲存",
		Color:       colorSuccess,
		Description: "日後使用 `/eat` 指令時，若未帶入坐標將自動套用此預設值。",
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🏷️ 位置別名",
				Value:  name,
				Inline: true,
			},
			{
				Name:   "📍 經緯度坐標",
				Value:  fmt.Sprintf("%.6f, %.6f", pref.Latitude, pref.Longitude),
				Inline: true,
			},
			{
				Name:   "🎯 預設半徑",
				Value:  fmt.Sprintf("%d 公尺", pref.Radius),
				Inline: true,
			},
		},
	}
}

// RenderErrorEmbed 產生錯誤提示 Embed
func RenderErrorEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("⚠️ %s", title),
		Description: description,
		Color:       colorError,
	}
}

// RenderHelpEmbed 產生指令說明 Embed
func RenderHelpEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "📖 餐廳抽籤機器人使用指南",
		Color:       colorInfo,
		Description: "隨機挑選附近營業中的餐廳，幫你解決「今天吃什麼」的難題！",
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "1️⃣ `/set` - 設定個人預設坐標（私密設定）",
				Value:  "`/set latitude:25.0339 longitude:121.5644 [radius:1000] [name:公司]`\n儲存後，後續抽籤可直接輸入 `/eat` 免重複打坐標。",
				Inline: false,
			},
			{
				Name:   "2️⃣ `/eat` - 隨機抽餐廳（公開結果）",
				Value:  "`/eat [keyword:拉麵] [latitude:25.0339] [longitude:121.5644] [radius:1500]`\n若不帶參數，則直接使用 `/set` 儲存的偏好。",
				Inline: false,
			},
			{
				Name:   "3️⃣ 🔄 零費用再抽一次 (Reroll)",
				Value:  "抽籤結果卡片下方有「再抽一家」按鈕，點擊可即時從同批清單再挑選，**完全不重複扣 Google API 額度**。",
				Inline: false,
			},
			{
				Name:   "💡 如何取得經緯度坐標？",
				Value:  "開啟 **Google 地圖** App 或網頁版，在你的位置或目標地點**長按**（或按右鍵），即可一鍵複製如 `25.0339, 121.5644` 的坐標。",
				Inline: false,
			},
		},
	}
}
