package lottery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"random_eat_discord/internal/db"
	"random_eat_discord/internal/googlemaps"
)

// 字串輸入長度上限（以字元計），同時用於 Slash Command 的 MaxLength 設定
const (
	MaxLocationNameLength = 32
	MaxKeywordLength      = 50
)

var (
	ErrNameTooLong            = fmt.Errorf("位置別名最多 %d 個字", MaxLocationNameLength)
	ErrKeywordTooLong         = fmt.Errorf("關鍵字最多 %d 個字", MaxKeywordLength)
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

// Candidate 候選餐廳與其距搜尋中心的距離（無坐標時為 -1）
type Candidate struct {
	Place          googlemaps.Place
	DistanceMeters float64
}

// ListResult 完整候選名單
type ListResult struct {
	Candidates []Candidate
	QueryCtx   QueryContext
}

// Service 抽籤核心領域介面
type Service interface {
	Draw(ctx context.Context, req DrawRequest) (*DrawResult, error)
	Reroll(ctx context.Context, sessionID string) (*DrawResult, error)
	List(ctx context.Context, req DrawRequest) (*ListResult, error)
	SetPreference(ctx context.Context, userID, name string, lat, lng float64, radius int) (*db.UserPreference, error)
	GetPreference(ctx context.Context, userID string) (*db.UserPreference, error)
	DeletePreference(ctx context.Context, userID string) error
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

// SetPreference 設定使用者預設位置與搜尋半徑，成功時回傳實際寫入的設定值
func (s *lotteryService) SetPreference(ctx context.Context, userID, name string, lat, lng float64, radius int) (*db.UserPreference, error) {
	if err := ValidateCoordinates(lat, lng); err != nil {
		return nil, err
	}
	if radius <= 0 {
		radius = 1000
	}
	if err := ValidateRadius(radius); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > MaxLocationNameLength {
		return nil, ErrNameTooLong
	}

	pref := &db.UserPreference{
		UserID:       userID,
		LocationName: name,
		Latitude:     lat,
		Longitude:    lng,
		Radius:       radius,
	}
	if err := s.repo.UpsertPreference(ctx, pref); err != nil {
		return nil, err
	}
	return pref, nil
}

// DeletePreference 刪除使用者儲存的預設位置
func (s *lotteryService) DeletePreference(ctx context.Context, userID string) error {
	return s.repo.DeletePreference(ctx, userID)
}

// GetPreference 取得使用者預設位置
func (s *lotteryService) GetPreference(ctx context.Context, userID string) (*db.UserPreference, error) {
	return s.repo.GetPreference(ctx, userID)
}

// Draw 執行餐廳抽籤
func (s *lotteryService) Draw(ctx context.Context, req DrawRequest) (*DrawResult, error) {
	places, qCtx, err := s.searchCandidates(ctx, req)
	if err != nil {
		return nil, err
	}

	// 建立 Session 並存入快取 (TTL: 10 分鐘)
	sessionID := uuid.New().String()
	s.sessionCache.Store(sessionID, places, qCtx, 10*time.Minute)

	// 抽取第 1 家
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

// List 回傳與 Draw 相同條件下的完整候選名單（依距離由近到遠排序），不建立 Session，供測試檢視用
func (s *lotteryService) List(ctx context.Context, req DrawRequest) (*ListResult, error) {
	places, qCtx, err := s.searchCandidates(ctx, req)
	if err != nil {
		return nil, err
	}

	candidates := make([]Candidate, 0, len(places))
	for _, p := range places {
		dist := -1.0
		if p.Location != nil {
			dist = googlemaps.Haversine(qCtx.Latitude, qCtx.Longitude, p.Location.Latitude, p.Location.Longitude)
		}
		candidates = append(candidates, Candidate{Place: p, DistanceMeters: dist})
	}
	// 依距離由近到遠，無坐標者排在最後
	sort.SliceStable(candidates, func(i, j int) bool {
		di, dj := candidates[i].DistanceMeters, candidates[j].DistanceMeters
		if (di < 0) != (dj < 0) {
			return dj < 0
		}
		return di < dj
	})

	return &ListResult{Candidates: candidates, QueryCtx: qCtx}, nil
}

// searchCandidates 解析坐標與半徑並呼叫 Google Places API，回傳抽籤候選名單（Draw 與 List 共用，確保兩者名單一致）
func (s *lotteryService) searchCandidates(ctx context.Context, req DrawRequest) ([]googlemaps.Place, QueryContext, error) {
	// 經緯度若提供則必須成對提供
	if (req.Latitude != nil && req.Longitude == nil) || (req.Latitude == nil && req.Longitude != nil) {
		return nil, QueryContext{}, ErrCoordinatePairRequired
	}
	if utf8.RuneCountInString(req.Keyword) > MaxKeywordLength {
		return nil, QueryContext{}, ErrKeywordTooLong
	}

	var targetLat, targetLng float64
	var targetRadius int
	var isCustomCoord bool
	var locationName string

	// 1. 解析坐標與半徑：優先採用即時輸入，次之查詢 DB 偏好
	if req.Latitude != nil && req.Longitude != nil {
		if err := ValidateCoordinates(*req.Latitude, *req.Longitude); err != nil {
			return nil, QueryContext{}, err
		}
		targetLat = *req.Latitude
		targetLng = *req.Longitude
		isCustomCoord = true
		locationName = "指定坐標"

		if req.Radius != nil {
			if err := ValidateRadius(*req.Radius); err != nil {
				return nil, QueryContext{}, err
			}
			targetRadius = *req.Radius
		} else {
			targetRadius = 1000
		}
	} else {
		// 查詢使用者偏好
		pref, err := s.repo.GetPreference(ctx, req.UserID)
		if err != nil {
			return nil, QueryContext{}, fmt.Errorf("查詢使用者偏好失敗: %w", err)
		}
		if pref == nil {
			return nil, QueryContext{}, ErrNoPreference
		}

		targetLat = pref.Latitude
		targetLng = pref.Longitude
		isCustomCoord = false
		// 隱私保護：在公開抽籤結果一律使用非私人語意標籤，不輸出使用者自訂的私人位置名稱（如「我家」）
		locationName = "預設位置"

		if req.Radius != nil {
			if err := ValidateRadius(*req.Radius); err != nil {
				return nil, QueryContext{}, err
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
		return nil, QueryContext{}, fmt.Errorf("搜尋餐廳失敗: %w", err)
	}

	if len(places) == 0 {
		return nil, QueryContext{}, ErrNoRestaurants
	}

	qCtx := QueryContext{
		Keyword:       req.Keyword,
		Latitude:      targetLat,
		Longitude:     targetLng,
		Radius:        targetRadius,
		LocationName:  locationName,
		IsCustomCoord: isCustomCoord,
		Initiator:     req.InitiatorName,
	}
	return places, qCtx, nil
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
