package config

import (
	"errors"
	"os"

	"github.com/joho/godotenv"
)

// Config 存放系統全域設定
type Config struct {
	DiscordToken     string
	DiscordAppID     string
	DiscordGuildID   string // 選填：用於特定伺服器即時測試 Slash Commands
	GoogleMapsAPIKey string
	DBPath           string
}

// Load 從環境變數或 .env 檔案載入設定
func Load() (*Config, error) {
	// 嘗試載入 .env 檔案（若不存在則忽略錯誤，直接讀取環境變數）
	_ = godotenv.Load()

	cfg := &Config{
		DiscordToken:     os.Getenv("DISCORD_TOKEN"),
		DiscordAppID:     os.Getenv("DISCORD_APP_ID"),
		DiscordGuildID:   os.Getenv("DISCORD_GUILD_ID"),
		GoogleMapsAPIKey: os.Getenv("GOOGLE_MAPS_API_KEY"),
		DBPath:           os.Getenv("DB_PATH"),
	}

	if cfg.DBPath == "" {
		cfg.DBPath = "./data/random_eat.db"
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate 驗證必要設定值
func (c *Config) Validate() error {
	if c.DiscordToken == "" {
		return errors.New("缺少必要的環境變數: DISCORD_TOKEN")
	}
	if c.DiscordAppID == "" {
		return errors.New("缺少必要的環境變數: DISCORD_APP_ID")
	}
	if c.GoogleMapsAPIKey == "" {
		return errors.New("缺少必要的環境變數: GOOGLE_MAPS_API_KEY")
	}
	return nil
}
