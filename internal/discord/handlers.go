package discord

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"random_eat_discord/internal/lottery"

	"github.com/bwmarrin/discordgo"
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
	// discordgo 不會攔截 handler 內的 panic，任一互動出錯都會讓整個程序崩潰，故在此統一攔截
	defer func() {
		if r := recover(); r != nil {
			slog.Error("處理 Interaction 時發生 panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()

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
	case "unset":
		h.handleUnsetCommand(s, i)
	case "eat-list":
		h.handleEatListCommand(s, i)
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

	pref, err := h.lotteryService.SetPreference(ctx, userID, name, lat, lng, radius)
	if err != nil {
		slog.Warn("設定偏好位置失敗", "user_id", userID, "error", err)
		editEmbed(s, i, RenderErrorEmbed("設定失敗", userFacingMessage(err)), nil)
		return
	}

	slog.Info("設定偏好位置成功", "user_id", userID, "latitude", pref.Latitude, "longitude", pref.Longitude, "radius", pref.Radius)
	editEmbed(s, i, RenderPreferenceEmbed(pref), nil)
}

func (h *Handler) handleUnsetCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Error("發送 Unset Deferred 回應失敗", "error", err)
		return
	}

	userID := getUserID(i)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	if err := h.lotteryService.DeletePreference(ctx, userID); err != nil {
		slog.Error("刪除偏好位置失敗", "user_id", userID, "error", err)
		editEmbed(s, i, RenderErrorEmbed("刪除失敗", userFacingMessage(err)), nil)
		return
	}

	slog.Info("刪除偏好位置成功", "user_id", userID)
	editEmbed(s, i, RenderUnsetEmbed(), nil)
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

	req := parseDrawRequest(i)

	slog.Info("收到抽籤請求", "user_id", req.UserID, "initiator", req.InitiatorName, "keyword", req.Keyword)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.lotteryService.Draw(ctx, req)
	if err != nil {
		slog.Error("抽籤失敗", "user_id", req.UserID, "initiator", req.InitiatorName, "keyword", req.Keyword, "error", err)
		replyErrorPrivately(s, i, RenderErrorEmbed("抽籤失敗", userFacingMessage(err)))
		return
	}

	restaurantName := ""
	address := ""
	if res != nil && res.Place != nil {
		restaurantName = res.Place.DisplayName.Text
		address = res.Place.FormattedAddress
	}
	slog.Info("抽籤成功", "user_id", req.UserID, "restaurant", restaurantName, "address", address)

	embed, components := RenderDrawEmbed(res)
	if !editEmbed(s, i, embed, components) {
		replyErrorPrivately(s, i, RenderErrorEmbed("抽籤失敗", "無法顯示抽籤結果，請稍後再試"))
	}
}

func (h *Handler) handleEatListCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// 測試用途：私密回應，避免完整名單洗版頻道
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		slog.Error("發送 Eat-List Deferred 回應失敗", "error", err)
		return
	}

	req := parseDrawRequest(i)
	slog.Info("收到候選名單請求", "user_id", req.UserID, "keyword", req.Keyword)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := h.lotteryService.List(ctx, req)
	if err != nil {
		slog.Error("取得候選名單失敗", "user_id", req.UserID, "keyword", req.Keyword, "error", err)
		editEmbed(s, i, RenderErrorEmbed("無法取得候選名單", userFacingMessage(err)), nil)
		return
	}

	slog.Info("取得候選名單成功", "user_id", req.UserID, "count", len(res.Candidates))
	editEmbed(s, i, RenderCandidateListEmbed(res), nil)
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

	caller := getUserDisplayName(i)
	callerID := getUserID(i)
	slog.Info("收到再抽請求", "user_id", callerID, "initiator", caller, "session_id", sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := h.lotteryService.Reroll(ctx, sessionID)
	if err != nil {
		slog.Warn("重新抽籤失敗或過期", "user_id", callerID, "session_id", sessionID, "error", err)
		// 清單過期或發生錯誤
		errorEmbed := RenderErrorEmbed("無法再抽", userFacingMessage(err))
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{errorEmbed},
			Flags:  discordgo.MessageFlagsEphemeral,
		})
		return
	}

	// 覆蓋發起者為當前點擊按鈕的使用者
	res.QueryCtx.Initiator = caller

	restaurantName := ""
	if res.Place != nil {
		restaurantName = res.Place.DisplayName.Text
	}
	slog.Info("重新抽籤成功", "user_id", callerID, "restaurant", restaurantName, "session_id", sessionID)

	embed, components := RenderDrawEmbed(res)
	editEmbed(s, i, embed, components)
}

// publicErrors 為可直接顯示給使用者的錯誤（訊息不含內部細節）
var publicErrors = []error{
	lottery.ErrNoPreference,
	lottery.ErrNoRestaurants,
	lottery.ErrInvalidCoordinate,
	lottery.ErrInvalidRadius,
	lottery.ErrCoordinatePairRequired,
	lottery.ErrSessionExpired,
	lottery.ErrNoMoreCandidates,
	lottery.ErrNameTooLong,
	lottery.ErrKeywordTooLong,
}

// userFacingMessage 將錯誤轉為可公開的訊息；內部錯誤（DB、Google API 回應等）只記錄於 log，不顯示給使用者
func userFacingMessage(err error) string {
	for _, e := range publicErrors {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "處理逾時，請稍後再試"
	}
	return "系統暫時無法處理請求，請稍後再試"
}

// editEmbed 更新 Deferred 回應內容，失敗時記錄 log 並回傳 false
func editEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) bool {
	edit := &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	}
	if components != nil {
		edit.Components = &components
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Error("更新 Interaction 回應失敗", "error", err)
		return false
	}
	return true
}

// replyErrorPrivately 移除公開的「思考中」訊息，改以僅發起者可見的訊息回報錯誤，避免錯誤洗版頻道
func replyErrorPrivately(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	if err := s.InteractionResponseDelete(i.Interaction); err != nil {
		slog.Warn("刪除公開 Deferred 回應失敗，改為直接編輯", "error", err)
		editEmbed(s, i, embed, nil)
		return
	}
	if _, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{embed},
		Flags:  discordgo.MessageFlagsEphemeral,
	}); err != nil {
		slog.Error("發送私密錯誤訊息失敗", "error", err)
	}
}

// 輔助工具函式

// parseDrawRequest 解析 /eat 與 /eat-list 共用之搜尋參數
func parseDrawRequest(i *discordgo.InteractionCreate) lottery.DrawRequest {
	options := parseOptions(i.ApplicationCommandData().Options)
	req := lottery.DrawRequest{
		UserID:        getUserID(i),
		InitiatorName: getUserDisplayName(i),
	}

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
	return req
}

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
