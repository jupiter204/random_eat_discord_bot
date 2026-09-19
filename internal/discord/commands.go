package discord

import (
	"github.com/bwmarrin/discordgo"
)

var (
	minRadius = 100.0
	maxRadius = 5000.0
	minLat    = -90.0
	maxLat    = 90.0
	minLng    = -180.0
	maxLng    = 180.0

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
					Description: "搜尋半徑 (公尺，預設 1000，範圍 100~5000)",
					Required:    false,
					MinValue:    &minRadius,
					MaxValue:    maxRadius,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "位置別名 (例如: 公司、家)",
					Required:    false,
				},
			},
		},
		{
			Name:        "eat",
			Description: "隨機抽取附近營業中的餐廳",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "keyword",
					Description: "指定食物種類或關鍵字 (例如: 拉麵、火鍋、義大利麵)",
					Required:    false,
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
			},
		},
		{
			Name:        "help",
			Description: "查看餐廳抽籤機器人使用教學與坐標取得方式",
		},
	}
)
