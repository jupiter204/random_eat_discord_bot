package discord

import (
	"github.com/bwmarrin/discordgo"
	"random_eat_discord/internal/lottery"
)

var (
	minRadius = 100.0
	maxRadius = 5000.0
	minLat    = -90.0
	maxLat    = 90.0
	minLng    = -180.0
	maxLng    = 180.0

	// searchOptions 為 /eat 與 /eat-list 共用之搜尋參數，確保兩者條件一致
	searchOptions = []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "keyword",
			Description: "指定食物種類或關鍵字 (例如: 拉麵、火鍋、義大利麵)",
			Required:    false,
			MaxLength:   lottery.MaxKeywordLength,
		},
		{
			Type:        discordgo.ApplicationCommandOptionNumber,
			Name:        "latitude",
			Description: "臨時指定緯度 (留空則使用 /set 預設值)",
			Required:    false,
			MinValue:    &minLat,
			MaxValue:    maxLat,
		},
		{
			Type:        discordgo.ApplicationCommandOptionNumber,
			Name:        "longitude",
			Description: "臨時指定經度 (留空則使用 /set 預設值)",
			Required:    false,
			MinValue:    &minLng,
			MaxValue:    maxLng,
		},
		{
			Type:        discordgo.ApplicationCommandOptionInteger,
			Name:        "radius",
			Description: "搜尋半徑 (公尺，預設使用個人偏好或 1000m)",
			Required:    false,
			MinValue:    &minRadius,
			MaxValue:    maxRadius,
		},
	}

	// eatListCommandName 為限定使用者的測試指令名稱
	eatListCommandName = "eat-list"

	// SlashCommands 定義所有 Slash Commands 規格
	SlashCommands = []*discordgo.ApplicationCommand{
		{
			Name:        "set",
			Description: "設定個人預設經緯度坐標與搜尋半徑（私密設定）",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "latitude",
					Description: "緯度 (例如: 25.0339)",
					Required:    true,
					MinValue:    &minLat,
					MaxValue:    maxLat,
				},
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "longitude",
					Description: "經度 (例如: 121.5644)",
					Required:    true,
					MinValue:    &minLng,
					MaxValue:    maxLng,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "radius",
					Description: "搜尋半徑 (公尺，範圍 100~5000；未填則沿用目前設定，首次預設 1000)",
					Required:    false,
					MinValue:    &minRadius,
					MaxValue:    maxRadius,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "位置別名 (例如: 公司、家；未填則沿用目前設定)",
					Required:    false,
					MaxLength:   lottery.MaxLocationNameLength,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "clear_name",
					Description: "清除已儲存的位置別名 (不可與 name 同時使用)",
					Required:    false,
				},
			},
		},
		{
			Name:        "eat",
			Description: "隨機抽取附近營業中的餐廳",
			Options:     searchOptions,
		},
		{
			Name:        eatListCommandName,
			Description: "（限指定測試者）列出抽籤候選名單中的全部餐廳，僅自己可見",
			Options:     searchOptions,
		},
		{
			Name:        "unset",
			Description: "刪除已儲存的個人預設坐標（私密操作）",
		},
		{
			Name:        "help",
			Description: "查看餐廳抽籤機器人使用教學與坐標取得方式",
		},
	}
)

// commandsToRegister 回傳實際要註冊的指令；未設定 /eat-list 允許名單時不註冊該指令
func commandsToRegister(eatListEnabled bool) []*discordgo.ApplicationCommand {
	cmds := make([]*discordgo.ApplicationCommand, 0, len(SlashCommands))
	for _, cmd := range SlashCommands {
		if cmd.Name == eatListCommandName && !eatListEnabled {
			continue
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}
