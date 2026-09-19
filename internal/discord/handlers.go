package discord

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"random_eat_discord/internal/lottery"
)

// Handler 封裝 Discord 事件處理器
type Handler struct {
	lotteryService lottery.Service
}

// NewHandler 建立事件處理器
func NewHandler(service lottery.Service) *Handler {
	return &Handler{
		lotteryService: service,
	}
}

// HandleInteraction 處理所有 Discord Interaction 事件
func (h *Handler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		h.handleApplicationCommand(s, i)
	case discordgo.InteractionMessageComponent:
		h.handleMessageComponent(s, i)
	}
}

func (h *Handler) handleApplicationCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cmdData := i.ApplicationCommandData()

	switch cmdData.Name {
	case "set":
		h.handleSetCommand(s, i)
	case "eat":
		h.handleEatCommand(s, i)
	case "help":
		h.handleHelpCommand(s, i)
	default:
		slog.Warn("收到未知的指令", "name", cmdData.Name)
	}
}

func (h *Handler) handleSetCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// 1. 立即回覆 Deferred Ephemeral，防止 3 秒逾時且保護使用者位置隱私
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Error("發送 Set Deferred 回應失敗", "error", err)
		return
	}

	options := parseOptions(i.ApplicationCommandData().Options)
	var lat, lng float64
	var radius int = 1000
	var name string

	if v, ok := options["latitude"]; ok {
		lat = v.FloatValue()
	}
	if v, ok := options["longitude"]; ok {
		lng = v.FloatValue()
	}
	if v, ok := options["radius"]; ok {
		radius = int(v.IntValue())
	}
	if v, ok := options["name"]; ok {
		name = v.StringValue()
	}

	userID := getUserID(i)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	err = h.lotteryService.SetPreference(ctx, userID, name, lat, lng, radius)
	if err != nil {
		embed := RenderErrorEmbed("設定失敗", err.Error())
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
		return
	}

	pref, _ := h.lotteryService.GetPreference(ctx, userID)
	embed := RenderPreferenceEmbed(pref)
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

func (h *Handler) handleEatCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// 1. 立即發送公開 Deferred 回應
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		slog.Error("發送 Eat Deferred 回應失敗", "error", err)
		return
	}

	options := parseOptions(i.ApplicationCommandData().Options)
	var req lottery.DrawRequest
	req.UserID = getUserID(i)
	req.InitiatorName = getUserDisplayName(i)

	if v, ok := options["keyword"]; ok {
		req.Keyword = strings.TrimSpace(v.StringValue())
	}
	if v, ok := options["latitude"]; ok {
		lat := v.FloatValue()
		req.Latitude = &lat
	}
	if v, ok := options["longitude"]; ok {
		lng := v.FloatValue()
		req.Longitude = &lng
	}
	if v, ok := options["radius"]; ok {
		r := int(v.IntValue())
		req.Radius = &r
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.lotteryService.Draw(ctx, req)
	if err != nil {
		embed := RenderErrorEmbed("抽籤失敗", err.Error())
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
		return
	}

	embed, components := RenderDrawEmbed(res)
	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &components,
	})
	if err != nil {
		slog.Error("更新抽籤結果訊息失敗", "error", err)
	}
}

func (h *Handler) handleHelpCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := RenderHelpEmbed()
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *Handler) handleMessageComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	if strings.HasPrefix(customID, "reroll:") {
		sessionID := strings.TrimPrefix(customID, "reroll:")
		h.handleReroll(s, i, sessionID)
	}
}

func (h *Handler) handleReroll(s *discordgo.Session, i *discordgo.InteractionCreate, sessionID string) {
	// 立即發送 Deferred Update
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
	if err != nil {
		slog.Error("發送 Reroll Deferred 回應失敗", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := h.lotteryService.Reroll(ctx, sessionID)
	if err != nil {
		// 清單過期或發生錯誤
		errorEmbed := RenderErrorEmbed("無法再抽", err.Error())
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{errorEmbed},
			Flags:  discordgo.MessageFlagsEphemeral,
		})
		return
	}

	// 覆蓋發起者為當前點擊按鈕的使用者
	res.QueryCtx.Initiator = getUserDisplayName(i)

	embed, components := RenderDrawEmbed(res)
	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &components,
	})
	if err != nil {
		slog.Error("更新 Reroll 結果失敗", "error", err)
	}
}

// 輔助工具函式
func parseOptions(options []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*discordgo.ApplicationCommandInteractionDataOption)
	for _, opt := range options {
		m[opt.Name] = opt
	}
	return m
}

func getUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func getUserDisplayName(i *discordgo.InteractionCreate) string {
	if i.Member != nil {
		if i.Member.Nick != "" {
			return i.Member.Nick
		}
		if i.Member.User != nil {
			if i.Member.User.GlobalName != "" {
				return i.Member.User.GlobalName
			}
			return i.Member.User.Username
		}
	}
	if i.User != nil {
		if i.User.GlobalName != "" {
			return i.User.GlobalName
		}
		return i.User.Username
	}
	return "未知使用者"
}
