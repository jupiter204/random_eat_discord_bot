package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DefaultRadius 首次建立偏好且未指定半徑時使用的預設值（公尺）
const DefaultRadius = 1000

// Repository 定義資料存取介面
type Repository interface {
	// UpsertPreference 新增或更新使用者偏好；name、radius 為 nil 時保留既有值（首次建立則為空別名與 DefaultRadius），
	// 合併與寫入在同一條 SQL 內完成，避免並行更新互相覆蓋。回傳寫入後的完整設定。
	UpsertPreference(ctx context.Context, userID string, lat, lng float64, name *string, radius *int) (*UserPreference, error)
	GetPreference(ctx context.Context, userID string) (*UserPreference, error)
	DeletePreference(ctx context.Context, userID string) error
}

type sqliteRepository struct {
	db *sql.DB
}

// NewRepository 建立 SQLite Repository 實例
func NewRepository(db *sql.DB) Repository {
	return &sqliteRepository{db: db}
}

// UpsertPreference 新增或更新使用者偏好（部分更新，單一 SQL 原子完成）
func (r *sqliteRepository) UpsertPreference(ctx context.Context, userID string, lat, lng float64, name *string, radius *int) (*UserPreference, error) {
	query := `
	INSERT INTO user_preferences (user_id, location_name, latitude, longitude, radius, updated_at)
	VALUES (?1, COALESCE(?2, ''), ?3, ?4, COALESCE(?5, ?6), CURRENT_TIMESTAMP)
	ON CONFLICT(user_id) DO UPDATE SET
		location_name = COALESCE(?2, location_name),
		latitude = excluded.latitude,
		longitude = excluded.longitude,
		radius = COALESCE(?5, radius),
		updated_at = CURRENT_TIMESTAMP
	RETURNING user_id, location_name, latitude, longitude, radius, updated_at;
	`
	// nil 指標以 SQL NULL 傳入，交由 COALESCE 保留既有值
	var nameArg, radiusArg any
	if name != nil {
		nameArg = *name
	}
	if radius != nil {
		radiusArg = *radius
	}

	var pref UserPreference
	err := r.db.QueryRowContext(ctx, query, userID, nameArg, lat, lng, radiusArg, DefaultRadius).
		Scan(&pref.UserID, &pref.LocationName, &pref.Latitude, &pref.Longitude, &pref.Radius, &pref.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert preference error: %w", err)
	}
	return &pref, nil
}

// GetPreference 依據使用者 ID 取得預設偏好設定
func (r *sqliteRepository) GetPreference(ctx context.Context, userID string) (*UserPreference, error) {
	query := `
	SELECT user_id, location_name, latitude, longitude, radius, updated_at
	FROM user_preferences
	WHERE user_id = ?;
	`
	row := r.db.QueryRowContext(ctx, query, userID)

	var pref UserPreference
	var updatedAt time.Time
	err := row.Scan(&pref.UserID, &pref.LocationName, &pref.Latitude, &pref.Longitude, &pref.Radius, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 查無偏好時回傳 nil, nil
		}
		return nil, fmt.Errorf("get preference error: %w", err)
	}
	pref.UpdatedAt = updatedAt
	return &pref, nil
}

// DeletePreference 刪除使用者偏好
func (r *sqliteRepository) DeletePreference(ctx context.Context, userID string) error {
	query := `DELETE FROM user_preferences WHERE user_id = ?;`
	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("delete preference error: %w", err)
	}
	return nil
}
