package lottery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	ErrNameBlank              = errors.New("位置別名不能只有空白；若要清除別名請使用 `clear_name:True`")
	ErrNoPreference           = errors.New("尚未設定預設位置，請先使用 `/set` 指令或在 `/eat` 帶入坐標")
	ErrNoRestaurants          = errors.New("在指定範圍內找不到營業中的餐廳，請嘗試擴大半徑或更換坐標")
	ErrInvalidCoordinate      = errors.New("坐標格式不正確 (緯度範圍: -90~90, 經度範圍: -180~180)")
	ErrInvalidRadius          = errors.New("搜尋半徑必須介於 100 至 5000 公尺之間")
	ErrCoordinatePairRequired = errors.New("latitude 與 longitude 必須同時提供")
	ErrSessionExpired         = errors.New("抽籤清單已過期或不存在，請重新輸入 `/eat` 進行抽籤")
	ErrNoMoreCandidates       = errors.New("沒有更多候選餐廳，請重新使用 /eat")
	ErrLoadingMore            = errors.New("正在載入更多餐廳，請稍後再按一次")
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
	// HasMore 為 true 代表目前這頁抽完後還能向 Google 載入下一頁
	HasMore  bool
	QueryCtx QueryContext
}

// maxSearchPages Google Text Search 最多提供 3 頁（共 60 筆）
const maxSearchPages = 3

// searchOutcome 為一次搜尋的候選名單與分頁狀態
type searchOutcome struct {
	places        []googlemaps.Place
	nextPageToken string
	pagesLoaded   int
	qCtx          QueryContext
}

// Candidate 候選餐廳與其距搜尋中心的距離（無坐標時為 -1）
type Candidate struct {
	Place          googlemaps.Place
	DistanceMeters float64
}

// ListResult 完整候選名單
type ListResult struct {
	Candidates []Candidate
	// HasMore 為 true 代表 Google 還有下一頁（/eat 抽完這頁後會再載入）
	HasMore  bool
	QueryCtx QueryContext
}

// Service 抽籤核心領域介面
type Service interface {
	Draw(ctx context.Context, req DrawRequest) (*DrawResult, error)
	Reroll(ctx context.Context, sessionID string) (*DrawResult, error)
	List(ctx context.Context, req DrawRequest) (*ListResult, error)
	SetPreference(ctx context.Context, userID string, name *string, lat, lng float64, radius *int) (*db.UserPreference, error)
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

// SetPreference 設定使用者預設位置；坐標必填，name 與 radius 為 nil 時沿用既有設定（首次設定則為空別名與 1000 公尺）。
// name 指向空字串代表清除別名；只含空白的別名視為輸入錯誤（避免誤清除）。合併與寫入由 Repository 以單一 SQL 原子完成。成功時回傳實際寫入的設定值。
func (s *lotteryService) SetPreference(ctx context.Context, userID string, name *string, lat, lng float64, radius *int) (*db.UserPreference, error) {
	if err := ValidateCoordinates(lat, lng); err != nil {
		return nil, err
	}
	if radius != nil {
		if err := ValidateRadius(*radius); err != nil {
			return nil, err
		}
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" && *name != "" {
			return nil, ErrNameBlank
		}
		if utf8.RuneCountInString(trimmed) > MaxLocationNameLength {
			return nil, ErrNameTooLong
		}
		name = &trimmed
	}

	return s.repo.UpsertPreference(ctx, userID, lat, lng, name, radius)
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
	out, err := s.searchCandidates(ctx, req)
	if err != nil {
		return nil, err
	}
	places, qCtx := out.places, out.qCtx

	// 建立 Session 並存入快取 (TTL: 10 分鐘)，一併記錄下一頁 token 供 Reroll 抽完時載入
	sessionID := uuid.New().String()
	s.sessionCache.Store(sessionID, places, qCtx, 10*time.Minute)
	s.sessionCache.SetNextPage(sessionID, out.nextPageToken, out.pagesLoaded)

	// 抽取第 1 家：搜尋到回應之間店家可能剛好打烊，抽不到時與 Reroll 相同地往下一頁找
	chosen, remaining, _, err := s.pickWithLoad(ctx, sessionID)
	if err != nil {
		s.sessionCache.Delete(sessionID)
		if errors.Is(err, ErrNoMoreCandidates) {
			return nil, ErrNoRestaurants
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("抽取餐廳失敗: %w", err)
		}
		// 第 1 頁的店剛好都打烊、載入下一頁又失敗：對使用者而言就是找不到可抽的餐廳，細節只記 log
		slog.Warn("第 1 頁無可抽店家且載入下一頁失敗", "error", err)
		return nil, ErrNoRestaurants
	}

	return &DrawResult{
		SessionID:      sessionID,
		Place:          chosen,
		RemainingCount: remaining,
		HasMore:        s.sessionCache.HasMorePages(sessionID),
		QueryCtx:       qCtx,
	}, nil
}

// List 回傳與 Draw 相同條件下的完整候選名單（依距離由近到遠排序），不建立 Session，供測試檢視用
func (s *lotteryService) List(ctx context.Context, req DrawRequest) (*ListResult, error) {
	out, err := s.searchCandidates(ctx, req)
	if err != nil {
		return nil, err
	}
	places, qCtx := out.places, out.qCtx

	// 與 PickNext 相同地跳過搜尋後已打烊的店，確保名單與 /eat 可抽到的店一致
	now := time.Now()
	candidates := make([]Candidate, 0, len(places))
	for _, p := range places {
		if p.IsClosedAt(now) {
			continue
		}
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

	if len(candidates) == 0 && out.nextPageToken == "" {
		return nil, ErrNoRestaurants
	}
	return &ListResult{Candidates: candidates, HasMore: out.nextPageToken != "", QueryCtx: qCtx}, nil
}

// searchCandidates 解析坐標與半徑並呼叫 Google Places API，回傳抽籤候選名單（Draw 與 List 共用，確保兩者名單一致）
func (s *lotteryService) searchCandidates(ctx context.Context, req DrawRequest) (searchOutcome, error) {
	// 經緯度若提供則必須成對提供
	if (req.Latitude != nil && req.Longitude == nil) || (req.Latitude == nil && req.Longitude != nil) {
		return searchOutcome{}, ErrCoordinatePairRequired
	}
	if utf8.RuneCountInString(req.Keyword) > MaxKeywordLength {
		return searchOutcome{}, ErrKeywordTooLong
	}

	var targetLat, targetLng float64
	var targetRadius int
	var isCustomCoord bool
	var locationName string

	// 1. 解析坐標與半徑：優先採用即時輸入，次之查詢 DB 偏好
	if req.Latitude != nil && req.Longitude != nil {
		if err := ValidateCoordinates(*req.Latitude, *req.Longitude); err != nil {
			return searchOutcome{}, err
		}
		targetLat = *req.Latitude
		targetLng = *req.Longitude
		isCustomCoord = true
		locationName = "指定坐標"

		if req.Radius != nil {
			if err := ValidateRadius(*req.Radius); err != nil {
				return searchOutcome{}, err
			}
			targetRadius = *req.Radius
		} else {
			targetRadius = 1000
		}
	} else {
		// 查詢使用者偏好
		pref, err := s.repo.GetPreference(ctx, req.UserID)
		if err != nil {
			return searchOutcome{}, fmt.Errorf("查詢使用者偏好失敗: %w", err)
		}
		if pref == nil {
			return searchOutcome{}, ErrNoPreference
		}

		targetLat = pref.Latitude
		targetLng = pref.Longitude
		isCustomCoord = false
		// 隱私保護：在公開抽籤結果一律使用非私人語意標籤，不輸出使用者自訂的私人位置名稱（如「我家」）
		locationName = "預設位置"

		if req.Radius != nil {
			if err := ValidateRadius(*req.Radius); err != nil {
				return searchOutcome{}, err
			}
			targetRadius = *req.Radius
		} else {
			targetRadius = pref.Radius
		}
	}

	// 2. 呼叫 Google Places API (New)：有無關鍵字皆使用相同的 Text Search 延遲分頁流程，
	//    只載入第 1 頁，下一頁留待 Reroll 抽完時再載入以節省 API 呼叫
	places, nextPageToken, pagesLoaded, err := s.fetchUntilNonEmpty(ctx, req.Keyword, targetLat, targetLng, targetRadius, "", 0)
	if err != nil {
		return searchOutcome{}, fmt.Errorf("搜尋餐廳失敗: %w", err)
	}

	if len(places) == 0 {
		return searchOutcome{}, ErrNoRestaurants
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
	return searchOutcome{places: places, nextPageToken: nextPageToken, pagesLoaded: pagesLoaded, qCtx: qCtx}, nil
}

// fetchUntilNonEmpty 從 pageToken 開始載入預設搜尋分頁，直到過濾後至少有 1 家、沒有下一頁或達到頁數上限為止。
// 避免某一頁剛好全被過濾掉（範圍外、非餐飲）時就誤判為沒有餐廳。回傳累計已載入頁數。
func (s *lotteryService) fetchUntilNonEmpty(ctx context.Context, keyword string, lat, lng float64, radius int, pageToken string, pagesLoaded int) ([]googlemaps.Place, string, int, error) {
	for pagesLoaded < maxSearchPages {
		places, next, err := s.mapsClient.SearchRestaurantsPage(ctx, keyword, lat, lng, radius, pageToken)
		if err != nil {
			if pagesLoaded > 0 {
				return nil, "", pagesLoaded, fmt.Errorf("取得第 %d 頁餐廳失敗: %w", pagesLoaded+1, err)
			}
			return nil, "", pagesLoaded, err
		}
		pagesLoaded++
		if len(places) > 0 || next == "" {
			if pagesLoaded >= maxSearchPages {
				next = ""
			}
			return places, next, pagesLoaded, nil
		}
		pageToken = next
	}
	return nil, "", pagesLoaded, nil
}

// Reroll 自 Session 中重抽下一家：目前這頁還有餐廳時不花費 API 額度；
// 這頁抽完且 Google 還有下一頁時，才載入下一頁後再抽。
func (s *lotteryService) Reroll(ctx context.Context, sessionID string) (*DrawResult, error) {
	chosen, remaining, qCtx, err := s.pickWithLoad(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	return &DrawResult{
		SessionID:      sessionID,
		Place:          chosen,
		RemainingCount: remaining,
		HasMore:        s.sessionCache.HasMorePages(sessionID),
		QueryCtx:       *qCtx,
	}, nil
}

// pickWithLoad 自 Session 抽一家；沒有可抽的店家時載入下一頁再抽，直到抽到、沒有下一頁或達頁數上限為止。
// 涵蓋下一頁全為重複店家、或新載入的店家已打烊等「載入了卻仍抽不到」的情況。
// 沒有下一頁時回傳 ErrNoMoreCandidates。
func (s *lotteryService) pickWithLoad(ctx context.Context, sessionID string) (*googlemaps.Place, int, *QueryContext, error) {
	for attempt := 0; ; attempt++ {
		chosen, remaining, qCtx, err := s.sessionCache.PickNext(sessionID)
		if !errors.Is(err, ErrNoMoreCandidates) || attempt >= maxSearchPages {
			return chosen, remaining, qCtx, err
		}
		if loadErr := s.loadNextPage(ctx, sessionID); loadErr != nil {
			return nil, 0, nil, loadErr
		}
	}
}

// loadNextPage 載入 Session 的下一頁並附加至候選名單；同一 Session 同時只會有一個請求在載入
func (s *lotteryService) loadNextPage(ctx context.Context, sessionID string) error {
	token, qCtx, pagesLoaded, err := s.sessionCache.ClaimNextPage(sessionID)
	if err != nil {
		return err
	}

	// 下一頁必須沿用第 1 頁的關鍵字，Google 的 pageToken 才有效
	places, next, newPagesLoaded, err := s.fetchUntilNonEmpty(ctx, qCtx.Keyword, qCtx.Latitude, qCtx.Longitude, qCtx.Radius, token, pagesLoaded)
	if err != nil {
		s.sessionCache.AbortNextPage(sessionID)
		return fmt.Errorf("載入下一頁餐廳失敗: %w", err)
	}

	s.sessionCache.CompleteNextPage(sessionID, places, next)
	// fetchUntilNonEmpty 可能連續跳過數個空頁，以實際累計頁數覆寫
	s.sessionCache.SetNextPage(sessionID, next, newPagesLoaded)
	return nil
}

// Close 釋放資源
func (s *lotteryService) Close() {
	s.sessionCache.Stop()
}
