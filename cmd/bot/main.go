package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"random_eat_discord/internal/config"
	"random_eat_discord/internal/db"
	"random_eat_discord/internal/discord"
	"random_eat_discord/internal/googlemaps"
	"random_eat_discord/internal/lottery"
)

func main() {
	// 設定 JSON 結構化日誌
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("正在啟動 Discord 餐廳抽籤機器人...")

	// 1. 載入組態設定
	cfg, err := config.Load()
	if err != nil {
		slog.Error("載入設定失敗", "error", err)
		os.Exit(1)
	}

	// 2. 初始化 SQLite 資料庫
	sqlDB, err := db.InitDB(cfg.DBPath)
	if err != nil {
		slog.Error("初始化資料庫失敗", "error", err)
		os.Exit(1)
	}
	defer sqlDB.Close()
	slog.Info("SQLite 資料庫初始化成功", "path", cfg.DBPath)

	// 3. 建立各層依賴
	repo := db.NewRepository(sqlDB)
	mapsClient := googlemaps.NewClient(cfg.GoogleMapsAPIKey)
	lotteryService := lottery.NewService(repo, mapsClient)
	defer lotteryService.Close()

	// 4. 初始化 Discord Bot
	bot, err := discord.NewBot(cfg, lotteryService)
	if err != nil {
		slog.Error("建立 Discord Bot 失敗", "error", err)
		os.Exit(1)
	}

	if err := bot.Start(); err != nil {
		slog.Error("啟動 Discord Bot 失敗", "error", err)
		os.Exit(1)
	}
	defer bot.Stop()

	slog.Info("機器人運行中，按 Ctrl+C 結束程式...")

	// 5. 等候中斷信號（Graceful Shutdown）
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	<-stopChan

	slog.Info("收到關閉信號，正在進行優雅關閉...")
}
