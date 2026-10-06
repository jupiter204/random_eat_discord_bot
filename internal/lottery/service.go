package lottery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"random_eat_discord/internal/db"
	"random_eat_discord/internal/googlemaps"
)

var (
	ErrNoPreference           = errors.New("尚未設定預設位置，請先使用 `/set` 指令或在 `/eat` 帶入坐標")
	ErrNoRestaurants          = errors.New("在指定範圍內找不到營業中的餐廳，請嘗試擴大半徑或更換坐標")
	ErrInvalidCoordinate      = errors.New("坐標格式不正確 (緯度範圍: -90~90, 經度範圍: -180~180)")
	ErrInvalidRadius          = errors.New("搜尋半徑必須介於 100 至 5000 公尺之間")
	ErrCoordinatePairRequired = errors.New("latitude 與 longitude 必須同時提供")
	ErrSessionExpired         = errors.New("抽籤清單已過期或不存在，請重新輸入 `/eat` 進行抽籤")
	ErrNoMoreCandidates       = errors.New("沒有更多候選餐廳，請重新使用 /eat")
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

// ValidateCoordinates 驗證經緯度是否在合法地理範圍內且非 NaN/Inf
func ValidateCoordinates(lat, lng float64) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lng) || math.IsInf(lng, 0) {
		return ErrInvalidCoordinate
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return ErrInvalidCoordinate
	}
	return nil
}

// ValidateRadius 驗證搜尋半徑範圍 (100 ~ 5000 公尺)
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
	// 經緯度若提供則必須成對提供
	if (req.Latitude != nil && req.Longitude == nil) || (req.Latitude == nil && req.Longitude != nil) {
		return nil, ErrCoordinatePairRequired
	}

	var targetLat, targetLng float64
	var targetRadius int
	var isCustomCoord bool
	var locationName string

	// 1. 解析坐標與半徑：優先採用即時輸入，次之查詢 DB 偏好
	if req.Latitude != nil && req.Longitude != nil {
		if err := ValidateCoordinates(*req.Latitude, *req.Longitude); err != nil {
			return nil, err
		}
		targetLat = *req.Latitude
		targetLng = *req.Longitude
		isCustomCoord = true
		locationName = "指定坐標"

		if req.Radius != nil {
			if err := ValidateRadius(*req.Radius); err != nil {
				return nil, err
			}
			targetRadius = *req.Radius
		} else {
			targetRadius = 1000
		}
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
		isCustomCoord = false
		// 隱私保護：在公開抽籤結果一律使用非私人語意標籤，不輸出使用者自訂的私人位置名稱（如「我家」）
		locationName = "預設位置"

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
		Keyword:       req.Keyword,
		Latitude:      targetLat,
		Longitude:     targetLng,
		Radius:        targetRadius,
		LocationName:  locationName,
		IsCustomCoord: isCustomCoord,
		Initiator:     req.InitiatorName,
	}

	s.sessionCache.Store(sessionID, places, qCtx, 10*time.Minute)

	// 4. 抽取第 1 家
	chosen, remaining, _, err := s.sessionCache.PickNext(sessionID)
	if err != nil {
		return nil, fmt.Errorf("抽取餐廳失敗: %w", err)
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
	chosen, remaining, qCtx, err := s.sessionCache.PickNext(sessionID)
	if err != nil {
		return nil, err
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
