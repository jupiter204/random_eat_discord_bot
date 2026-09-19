package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Repository 定義資料存取介面
type Repository interface {
	UpsertPreference(ctx context.Context, pref *UserPreference) error
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

// UpsertPreference 新增或更新使用者偏好
func (r *sqliteRepository) UpsertPreference(ctx context.Context, pref *UserPreference) error {
	query := `
	INSERT INTO user_preferences (user_id, location_name, latitude, longitude, radius, updated_at)
	VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(user_id) DO UPDATE SET
		location_name = excluded.location_name,
		latitude = excluded.latitude,
		longitude = excluded.longitude,
		radius = excluded.radius,
		updated_at = CURRENT_TIMESTAMP;
	`
	_, err := r.db.ExecContext(ctx, query, pref.UserID, pref.LocationName, pref.Latitude, pref.Longitude, pref.Radius)
	if err != nil {
		return fmt.Errorf("upsert preference error: %w", err)
	}
	return nil
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
