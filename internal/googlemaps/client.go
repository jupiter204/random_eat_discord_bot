package googlemaps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultFieldMask = "places.id,places.types,places.displayName,places.formattedAddress,places.location,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel,places.currentOpeningHours,places.regularOpeningHours,places.utcOffsetMinutes,places.timeZone,nextPageToken"
	textFieldMask    = "places.id,places.displayName,places.formattedAddress,places.location,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel,places.currentOpeningHours,places.regularOpeningHours,places.utcOffsetMinutes,places.timeZone,nextPageToken"
)

// FoodPlaceTypes 為預設搜尋視為餐飲的店家類型（Google Places Table A）。
// 僅用 restaurant 會漏掉被 Google 歸類為咖啡廳、外帶店、麵包店的用餐選擇；
// 各類餐廳子類型（如 ramen_restaurant）另由 IsFoodPlace 以 *_restaurant 後綴判斷。
var FoodPlaceTypes = []string{"restaurant", "cafe", "meal_takeaway", "bakery"}

// Client 定義 Google Places API 用戶端介面
// 無關鍵字時的預設搜尋設定（Text Search 每頁最多 20 筆）
const (
	DefaultTextQuery      = "餐廳"
	defaultSearchPageSize = 20
)

type Client interface {
	SearchOpenRestaurantsPage(ctx context.Context, lat, lng float64, radiusMeters int, pageToken string) ([]Place, string, error)
	SearchTextRestaurants(ctx context.Context, query string, lat, lng float64, radiusMeters int) ([]Place, error)
}

type placesClient struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
	maxRetries int
	baseDelay  time.Duration
}

// NewClient 建立 Google Places API 客戶端實例
func NewClient(apiKey string) Client {
	return newClientWithBaseURL(apiKey, "https://places.googleapis.com", 2, 100*time.Millisecond)
}

func newClientWithBaseURL(apiKey, baseURL string, maxRetries int, baseDelay time.Duration) *placesClient {
	return &placesClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL:    baseURL,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
	}
}

// NewTestClient 供單元測試自訂伺服器與重試參數
func NewTestClient(apiKey, baseURL string, maxRetries int, baseDelay time.Duration, httpClient *http.Client) Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &placesClient{
		apiKey:     apiKey,
		httpClient: httpClient,
		baseURL:    baseURL,
		maxRetries: maxRetries,
		baseDelay:  baseDelay,
	}
}

// SearchTextRestaurants 搜尋指定關鍵字且位於該坐標偏好範圍內的營業中餐廳 (使用 searchText)
func (c *placesClient) SearchTextRestaurants(ctx context.Context, query string, lat, lng float64, radiusMeters int) ([]Place, error) {
	reqBody := TextSearchRequest{
		TextQuery:           query,
		IncludedType:        "restaurant",
		StrictTypeFiltering: true,
		OpenNow:             true,
		LocationBias: &LocationBias{
			Circle: Circle{
				Center: LatLng{
					Latitude:  lat,
					Longitude: lng,
				},
				Radius: float64(radiusMeters),
			},
		},
	}

	endpoint := c.baseURL + "/v1/places:searchText"
	resp, err := c.doPost(ctx, endpoint, reqBody, textFieldMask)
	if err != nil {
		return nil, err
	}

	// 應用層過濾：營業中與指定半徑過濾
	openPlaces := FilterOpenPlaces(resp.Places)
	filtered := FilterByRadius(openPlaces, lat, lng, radiusMeters)
	logFilterStats("text", 1, resp.Places, len(openPlaces), len(filtered))

	// 若第 1 頁候選經半徑過濾後為 0 筆且存在 nextPageToken，最多抓取第 2 頁，避免因 Google API 排序將遠處店家排於首頁導致誤判無結果
	const maxPages = 2
	currentPage := 1
	nextPageToken := resp.NextPageToken

	for len(filtered) == 0 && nextPageToken != "" && currentPage < maxPages {
		currentPage++
		pageReq := reqBody
		pageReq.PageToken = nextPageToken

		pageResp, pageErr := c.doPost(ctx, endpoint, pageReq, textFieldMask)
		if pageErr != nil {
			return nil, fmt.Errorf("取得第 %d 頁餐廳失敗: %w", currentPage, pageErr)
		}

		pageOpen := FilterOpenPlaces(pageResp.Places)
		pageFiltered := FilterByRadius(pageOpen, lat, lng, radiusMeters)
		logFilterStats("text", currentPage, pageResp.Places, len(pageOpen), len(pageFiltered))
		filtered = append(filtered, pageFiltered...)
		nextPageToken = pageResp.NextPageToken
	}

	return filtered, nil
}

// FilterStats 為單次 API 回應在各過濾階段的店家數量，用於診斷候選名單為何變少
type FilterStats struct {
	Raw         int // Google 原始回傳筆數
	OpenNow     int // 確認營業中
	Closed      int // 明確標示休息中
	UnknownOpen int // 無營業時間資料（亦會被排除）
	InRadius    int // 營業中且位於半徑內（最終候選）
}

// ComputeFilterStats 統計各過濾階段數量；openCount 與 inRadius 由呼叫端的實際過濾結果傳入，確保與真實行為一致
func ComputeFilterStats(raw []Place, openCount, inRadius int) FilterStats {
	st := FilterStats{Raw: len(raw), OpenNow: openCount, InRadius: inRadius}
	for _, p := range raw {
		if p.CurrentOpeningHours == nil || p.CurrentOpeningHours.OpenNow == nil {
			st.UnknownOpen++
		} else if !*p.CurrentOpeningHours.OpenNow {
			st.Closed++
		}
	}
	return st
}

func logFilterStats(endpoint string, page any, raw []Place, openCount, inRadius int) {
	st := ComputeFilterStats(raw, openCount, inRadius)
	slog.Info("Places API 過濾統計",
		"endpoint", endpoint,
		"page", page,
		"raw", st.Raw,
		"open_now", st.OpenNow,
		"closed", st.Closed,
		"unknown_hours", st.UnknownOpen,
		"in_radius", st.InRadius,
	)
}

// SearchOpenRestaurantsPage 無關鍵字時的預設搜尋（單頁）：以 Text Search 搜尋「餐廳」並由 API 端過濾營業中（openNow），
// 範圍以涵蓋圓形半徑的矩形硬性限制；回傳前於應用層過濾餐飲類型、營業中與實際半徑。
// pageToken 為空時取第 1 頁；回傳之 nextPageToken 為空代表已無下一頁（Google 最多提供 3 頁共 60 筆）。
// 分頁請求必須與第 1 頁使用完全相同的參數，故由本函式統一組裝。
func (c *placesClient) SearchOpenRestaurantsPage(ctx context.Context, lat, lng float64, radiusMeters int, pageToken string) ([]Place, string, error) {
	reqBody := TextSearchRequest{
		TextQuery: DefaultTextQuery,
		OpenNow:   true,
		PageSize:  defaultSearchPageSize,
		PageToken: pageToken,
		LocationRestriction: &RectangleRestriction{
			Rectangle: BoundingRectangle(lat, lng, radiusMeters),
		},
	}

	resp, err := c.doPost(ctx, c.baseURL+"/v1/places:searchText", reqBody, defaultFieldMask)
	if err != nil {
		return nil, "", err
	}

	food := FilterFoodPlaces(resp.Places)
	open := FilterOpenPlaces(food)
	inRadius := FilterByRadius(open, lat, lng, radiusMeters)
	logFilterStats("text-default", pageLabel(pageToken), resp.Places, len(open), len(inRadius))
	if dropped := len(resp.Places) - len(food); dropped > 0 {
		slog.Info("排除非餐飲類型店家", "count", dropped)
	}

	return inRadius, resp.NextPageToken, nil
}

// pageLabel 供 log 區分首頁與後續分頁（實際頁序由 lottery 層掌握）
func pageLabel(pageToken string) string {
	if pageToken == "" {
		return "first"
	}
	return "next"
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

func (c *placesClient) doPost(ctx context.Context, url string, payload any, mask string) (*PlacesResponse, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 API 請求失敗: %w", err)
	}

	var lastErr error
	maxAttempts := 1 + c.maxRetries

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := c.baseDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBytes))
		if err != nil {
			return nil, fmt.Errorf("建立 HTTP 請求失敗: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Goog-Api-Key", c.apiKey)
		req.Header.Set("X-Goog-FieldMask", mask)
		req.Header.Set("X-Goog-Language-Code", "zh-TW")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("呼叫 Places API 失敗: %w", err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("讀取 Places API 回應失敗: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("Places API 回傳錯誤 (Status %d): %s", resp.StatusCode, string(respBody))
			if isRetryableStatus(resp.StatusCode) {
				continue
			}
			// 非暫態錯誤（例如 400, 401, 403, 404）不進行重試，直接中斷
			return nil, lastErr
		}

		var placesResp PlacesResponse
		if err := json.Unmarshal(respBody, &placesResp); err != nil {
			return nil, fmt.Errorf("解析 Places API 回應失敗: %w", err)
		}

		return &placesResp, nil
	}

	return nil, lastErr
}
