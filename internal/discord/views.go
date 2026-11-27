package discord

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"random_eat_discord/internal/db"
	"random_eat_discord/internal/lottery"

	"github.com/bwmarrin/discordgo"
)

const (
	colorSuccess = 0xF59E0B // 活力橘黃
	colorInfo    = 0x3B82F6 // 科技藍
	colorError   = 0xEF4444 // 警示紅
)

// Discord Embed 長度限制，超過會導致整則訊息被拒絕
const (
	maxTitleLen       = 256
	maxDescriptionLen = 4096
	maxFieldValueLen  = 1024
	maxFooterLen      = 2048
)

// truncate 依字元數截斷字串，超出時以「…」結尾
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// fieldValue 產生合法的 Embed Field 值：空白時使用 fallback（Discord 不接受空值），並截斷至長度上限
func fieldValue(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		s = fallback
	}
	return truncate(s, maxFieldValueLen)
}

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
			Value:  fieldValue(place.FormattedAddress, "未提供地址"),
			Inline: false,
		},
		{
			Name:   "⏰ 營業時間",
			Value:  fieldValue(place.TodayOpeningHours(), "未提供營業時間"),
			Inline: false,
		},
		{
			Name: "🎯 搜尋條件",
			Value: fieldValue(fmt.Sprintf("搜尋中心：%s\n搜尋範圍：半徑 %d 公尺%s",
				res.QueryCtx.LocationName,
				res.QueryCtx.Radius,
				func() string {
					if res.QueryCtx.Keyword != "" {
						return fmt.Sprintf("\n關鍵字：%s", res.QueryCtx.Keyword)
					}
					return ""
				}(),
			), "—"),
			Inline: false,
		},
	}

	footerText := fmt.Sprintf("剩餘 %d 家備選餐廳", res.RemainingCount)
	if res.QueryCtx.Initiator != "" {
		footerText = fmt.Sprintf("由 %s 發起 • %s", res.QueryCtx.Initiator, footerText)
	}

	placeName := strings.TrimSpace(place.DisplayName.Text)
	if placeName == "" {
		placeName = "未命名餐廳"
	}

	embed := &discordgo.MessageEmbed{
		Title:       truncate(fmt.Sprintf("🍽️ %s", placeName), maxTitleLen),
		URL:         place.GoogleMapsURI,
		Description: truncate(description, maxDescriptionLen),
		Color:       colorSuccess,
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: truncate(footerText, maxFooterLen),
		},
	}

	rerollBtn := discordgo.Button{
		Label:    "🔄 再抽一家 (不消耗 API)",
		Style:    discordgo.PrimaryButton,
		CustomID: fmt.Sprintf("reroll:%s", res.SessionID),
	}
	if res.RemainingCount == 0 {
		rerollBtn.Disabled = true
		rerollBtn.Label = "已無更多備選餐廳"
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				rerollBtn,
			},
		},
	}

	return embed, components
}

const preferenceSavedDescription = "日後使用 `/eat` 指令時，若未帶入坐標將自動套用此預設值。\n\n" +
	"🔒 注意：`/eat` 結果為公開訊息，抽中的餐廳位於預設位置附近，可能間接透露你的大致所在區域。如需刪除設定，請使用 `/unset`。"

// RenderPreferenceEmbed 產生預設偏好設定成功的 Embed
func RenderPreferenceEmbed(pref *db.UserPreference) *discordgo.MessageEmbed {
	name := pref.LocationName
	if name == "" {
		name = "（未命名）"
	}

	return &discordgo.MessageEmbed{
		Title:       "✅ 預設搜尋坐標已成功儲存",
		Color:       colorSuccess,
		Description: preferenceSavedDescription,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🏷️ 位置別名",
				Value:  fieldValue(name, "（未命名）"),
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

// RenderCandidateListEmbed 產生完整候選名單 Embed（測試用），依距離排序並於超出長度上限時標示未顯示筆數
func RenderCandidateListEmbed(res *lottery.ListResult) *discordgo.MessageEmbed {
	// 方括號會破壞 Markdown 連結語法，改為全形
	linkSafe := strings.NewReplacer("[", "［", "]", "］")

	var b strings.Builder
	shown := 0
	for idx, c := range res.Candidates {
		p := c.Place
		name := strings.TrimSpace(p.DisplayName.Text)
		if name == "" {
			name = "未命名餐廳"
		}
		name = linkSafe.Replace(name)
		if p.GoogleMapsURI != "" {
			name = fmt.Sprintf("[%s](%s)", name, p.GoogleMapsURI)
		}

		dist := "距離未知"
		if c.DistanceMeters >= 0 {
			dist = fmt.Sprintf("%dm", int(c.DistanceMeters+0.5))
		}
		rating := "無評分"
		if p.Rating > 0 {
			rating = fmt.Sprintf("⭐%.1f", p.Rating)
		}

		line := fmt.Sprintf("`%2d.` %s • %s • %s • %s\n", idx+1, name, dist, rating, p.FormattedPriceLevel())
		// 預留尾端「另有 N 家未顯示」提示空間
		if utf8.RuneCountInString(b.String())+utf8.RuneCountInString(line) > maxDescriptionLen-40 {
			break
		}
		b.WriteString(line)
		shown++
	}
	if hidden := len(res.Candidates) - shown; hidden > 0 {
		fmt.Fprintf(&b, "…另有 %d 家未顯示", hidden)
	}

	conditions := fmt.Sprintf("搜尋中心：%s • 半徑 %d 公尺", res.QueryCtx.LocationName, res.QueryCtx.Radius)
	if res.QueryCtx.Keyword != "" {
		conditions += " • 關鍵字：" + res.QueryCtx.Keyword
	}

	return &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("📋 抽籤候選名單（共 %d 家）", len(res.Candidates)),
		Description: b.String(),
		Color:       colorInfo,
		Footer: &discordgo.MessageEmbedFooter{
			Text: truncate(conditions+" • 依距離排序", maxFooterLen),
		},
	}
}

// RenderUnsetEmbed 產生刪除預設坐標成功的 Embed
func RenderUnsetEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🗑️ 已刪除預設搜尋坐標",
		Color:       colorInfo,
		Description: "你的位置資料已從資料庫移除。之後使用 `/eat` 需帶入坐標，或重新以 `/set` 設定。",
	}
}

// RenderErrorEmbed 產生錯誤提示 Embed
func RenderErrorEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       truncate(fmt.Sprintf("⚠️ %s", title), maxTitleLen),
		Description: truncate(description, maxDescriptionLen),
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
				Name:   "🧪 `/eat-list` - 查看完整候選名單（測試用）",
				Value:  "參數與 `/eat` 相同，列出這次會被抽的所有餐廳（依距離排序），僅自己可見。",
				Inline: false,
			},
			{
				Name:   "🗑️ `/unset` - 刪除個人預設坐標",
				Value:  "不想再保存位置資料時，可隨時刪除已儲存的預設坐標。",
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
