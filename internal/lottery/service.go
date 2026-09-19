package lottery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"random_eat_discord/internal/db"
	"random_eat_discord/internal/googlemaps"
)

var (
	ErrNoPreference      = errors.New("尚未設定預設位置，請先使用 `/set` 指令或在 `/eat` 帶入坐標")
	ErrNoRestaurants     = errors.New("在指定範圍內找不到營業中的餐廳，請嘗試擴大半徑或更換坐標")
	ErrInvalidCoordinate = errors.New("坐標格式不正確 (緯度範圍: -90~90, 經度範圍: -180~180)")
	ErrInvalidRadius     = errors.New("搜尋半徑必須介於 100 至 5000 公尺之間")
	ErrSessionExpired    = errors.New("抽籤清單已過期或不存在，請重新輸入 `/eat` 進行抽籤")
)

// DrawRequest 抽籤請求參數
type DrawRequest struct {
	UserID        string
	Keyword       string
	Latitude      *float64
	Longitude     *float64
	Radius        *int
	InitiatorName string
}

// DrawResult 抽籤結果
type DrawResult struct {
	SessionID      string
	Place          *googlemaps.Place
	RemainingCount int
	TotalCount     int
	QueryCtx       QueryContext
}

// Service 抽籤核心領域介面
type Service interface {
	Draw(ctx context.Context, req DrawRequest) (*DrawResult, error)
	Reroll(ctx context.Context, sessionID string) (*DrawResult, error)
	SetPreference(ctx context.Context, userID, name string, lat, lng float64, radius int) error
	GetPreference(ctx context.Context, userID string) (*db.UserPreference, error)
	Close()
}

type lotteryService struct {
	repo         db.Repository
	mapsClient   googlemaps.Client
	sessionCache SessionCache
}

// NewService 建立抽籤服務實例
func NewService(repo db.Repository, mapsClient googlemaps.Client) Service {
	return &lotteryService{
		repo:         repo,
		mapsClient:   mapsClient,
		sessionCache: NewSessionCache(),
	}
}

// ValidateCoordinates 驗證經緯度是否在合法地理範圍內
func ValidateCoordinates(lat, lng float64) error {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return ErrInvalidCoordinate
	}
	return nil
}

// ValidateRadius 驗證搜尋半徑範圍
func ValidateRadius(radius int) error {
	if radius < 100 || radius > 5000 {
		return ErrInvalidRadius
	}
	return nil
}

// SetPreference 設定使用者預設位置與搜尋半徑
func (s *lotteryService) SetPreference(ctx context.Context, userID, name string, lat, lng float64, radius int) error {
	if err := ValidateCoordinates(lat, lng); err != nil {
		return err
	}
	if radius <= 0 {
		radius = 1000
	}
	if err := ValidateRadius(radius); err != nil {
		return err
	}

	pref := &db.UserPreference{
		UserID:       userID,
		LocationName: name,
		Latitude:     lat,
		Longitude:    lng,
		Radius:       radius,
	}
	return s.repo.UpsertPreference(ctx, pref)
}

// GetPreference 取得使用者預設位置
func (s *lotteryService) GetPreference(ctx context.Context, userID string) (*db.UserPreference, error) {
	return s.repo.GetPreference(ctx, userID)
}

// Draw 執行餐廳抽籤
func (s *lotteryService) Draw(ctx context.Context, req DrawRequest) (*DrawResult, error) {
	var targetLat, targetLng float64
	var targetRadius int
	var locationName string

	// 1. 解析坐標與半徑：優先採用即時輸入，次之查詢 DB 偏好
	if req.Latitude != nil && req.Longitude != nil {
		if err := ValidateCoordinates(*req.Latitude, *req.Longitude); err != nil {
			return nil, err
		}
		targetLat = *req.Latitude
		targetLng = *req.Longitude

		if req.Radius != nil {
			if err := ValidateRadius(*req.Radius); err != nil {
				return nil, err
			}
			targetRadius = *req.Radius
		} else {
			targetRadius = 1000
		}
		locationName = fmt.Sprintf("即時坐標 (%.4f, %.4f)", targetLat, targetLng)
	} else {
		// 查詢使用者偏好
		pref, err := s.repo.GetPreference(ctx, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("查詢使用者偏好失敗: %w", err)
		}
		if pref == nil {
			return nil, ErrNoPreference
		}

		targetLat = pref.Latitude
		targetLng = pref.Longitude
		locationName = pref.LocationName
		if locationName == "" {
			locationName = fmt.Sprintf("預設坐標 (%.4f, %.4f)", targetLat, targetLng)
		}

		if req.Radius != nil {
			if err := ValidateRadius(*req.Radius); err != nil {
				return nil, err
			}
			targetRadius = *req.Radius
		} else {
			targetRadius = pref.Radius
		}
	}

	// 2. 呼叫 Google Places API (New)
	var places []googlemaps.Place
	var err error

	if req.Keyword != "" {
		places, err = s.mapsClient.SearchTextRestaurants(ctx, req.Keyword, targetLat, targetLng, targetRadius)
	} else {
		places, err = s.mapsClient.SearchNearbyRestaurants(ctx, targetLat, targetLng, targetRadius)
	}

	if err != nil {
		return nil, fmt.Errorf("搜尋餐廳失敗: %w", err)
	}

	if len(places) == 0 {
		return nil, ErrNoRestaurants
	}

	// 3. 建立 Session 並存入快取 (TTL: 10 分鐘)
	sessionID := uuid.New().String()
	qCtx := QueryContext{
		Keyword:      req.Keyword,
		Latitude:     targetLat,
		Longitude:    targetLng,
		Radius:       targetRadius,
		LocationName: locationName,
		Initiator:    req.InitiatorName,
	}

	s.sessionCache.Store(sessionID, places, qCtx, 10*time.Minute)

	// 4. 抽取第 1 家
	chosen, remaining, _, ok := s.sessionCache.PickNext(sessionID)
	if !ok {
		return nil, errors.New("抽取餐廳失敗")
	}

	return &DrawResult{
		SessionID:      sessionID,
		Place:          chosen,
		RemainingCount: remaining,
		TotalCount:     len(places),
		QueryCtx:       qCtx,
	}, nil
}

// Reroll 自記憶體 Session 中重抽下一家 (不花費 API 額度)
func (s *lotteryService) Reroll(ctx context.Context, sessionID string) (*DrawResult, error) {
	chosen, remaining, qCtx, ok := s.sessionCache.PickNext(sessionID)
	if !ok {
		return nil, ErrSessionExpired
	}

	return &DrawResult{
		SessionID:      sessionID,
		Place:          chosen,
		RemainingCount: remaining,
		QueryCtx:       *qCtx,
	}, nil
}

// Close 釋放資源
func (s *lotteryService) Close() {
	s.sessionCache.Stop()
}
