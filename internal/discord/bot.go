package discord

import (
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"random_eat_discord/internal/config"
	"random_eat_discord/internal/lottery"
)

// Bot 封裝 Discord 機器人連線與生命週期
type Bot struct {
	session            *discordgo.Session
	cfg                *config.Config
	handler            *Handler
	registeredCommands []*discordgo.ApplicationCommand
}

// NewBot 建立 Discord Bot 實例
func NewBot(cfg *config.Config, lotteryService lottery.Service) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("建立 Discord Session 失敗: %w", err)
	}

	handler := NewHandler(lotteryService)
	session.AddHandler(handler.HandleInteraction)

	return &Bot{
		session: session,
		cfg:     cfg,
		handler: handler,
	}, nil
}

// Start 啟動 Discord 機器人並註冊 Slash Commands
func (b *Bot) Start() error {
	b.session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		slog.Info("Discord 機器人已成功連線", "username", s.State.User.Username, "tag", s.State.User.Discriminator)
	})

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("開啟 Discord 連線失敗: %w", err)
	}

	// 註冊 Slash Commands (若指定 GuildID 則註冊至測試伺服器以加速生效，否則註冊全域指令)
	slog.Info("正在註冊 Slash Commands...", "guildID", b.cfg.DiscordGuildID)
	for _, cmd := range SlashCommands {
		createdCmd, err := b.session.ApplicationCommandCreate(b.cfg.DiscordAppID, b.cfg.DiscordGuildID, cmd)
		if err != nil {
			slog.Error("註冊 Slash Command 失敗", "name", cmd.Name, "error", err)
			return fmt.Errorf("註冊指令 %s 失敗: %w", cmd.Name, err)
		}
		b.registeredCommands = append(b.registeredCommands, createdCmd)
		slog.Info("Slash Command 註冊成功", "name", createdCmd.Name, "id", createdCmd.ID)
	}

	return nil
}

// Stop 停止機器人並清理已註冊指令（僅清理指定的 Guild 測試指令）
func (b *Bot) Stop() {
	if b.cfg.DiscordGuildID != "" {
		slog.Info("正在清理測試伺服器 Slash Commands...")
		for _, cmd := range b.registeredCommands {
			_ = b.session.ApplicationCommandDelete(b.cfg.DiscordAppID, b.cfg.DiscordGuildID, cmd.ID)
		}
	}

	slog.Info("正在關閉 Discord 連線...")
	_ = b.session.Close()
}
