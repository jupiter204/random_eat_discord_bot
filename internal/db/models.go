package db

import "time"

// UserPreference 儲存使用者的預設位置與搜尋偏好
type UserPreference struct {
	UserID       string    `json:"user_id"`
	LocationName string    `json:"location_name"`
	Latitude     float64   `json:"latitude"`
	Longitude    float64   `json:"longitude"`
	Radius       int       `json:"radius"`
	UpdatedAt    time.Time `json:"updated_at"`
}
