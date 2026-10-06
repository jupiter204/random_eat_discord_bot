package googlemaps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	fieldMask = "places.id,places.displayName,places.formattedAddress,places.location,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel,places.currentOpeningHours,places.regularOpeningHours,places.utcOffsetMinutes,places.timeZone,nextPageToken"
)

// Client 定義 Google Places API 用戶端介面
type Client interface {
	SearchNearbyRestaurants(ctx context.Context, lat, lng float64, radiusMeters int) ([]Place, error)
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

// SearchNearbyRestaurants 搜尋指定坐標與半徑內營業中的餐廳 (使用 searchNearby)
// 注意：Google Places API (New) 的 searchNearby 不支援 openNow 請求參數，營業狀態過濾由取得 currentOpeningHours 後在應用層完成
func (c *placesClient) SearchNearbyRestaurants(ctx context.Context, lat, lng float64, radiusMeters int) ([]Place, error) {
	reqBody := NearbySearchRequest{
		IncludedTypes:  []string{"restaurant"},
		MaxResultCount: 20,
		LocationRestriction: LocationRestriction{
			Circle: Circle{
				Center: LatLng{
					Latitude:  lat,
					Longitude: lng,
				},
				Radius: float64(radiusMeters),
			},
		},
	}

	endpoint := c.baseURL + "/v1/places:searchNearby"
	resp, err := c.doPost(ctx, endpoint, reqBody)
	if err != nil {
		return nil, err
	}

	// 確保應用層過濾：僅保留確認營業中之店家 (基於 currentOpeningHours.openNow == true)
	openPlaces := FilterOpenPlaces(resp.Places)
	// 嚴格半徑過濾：確保回傳店家在指定半徑內
	return FilterByRadius(openPlaces, lat, lng, radiusMeters), nil
}

// SearchTextRestaurants 搜尋指定關鍵字且位於該坐標偏好範圍內的營業中餐廳 (使用 searchText)
func (c *placesClient) SearchTextRestaurants(ctx context.Context, query string, lat, lng float64, radiusMeters int) ([]Place, error) {
	reqBody := TextSearchRequest{
		TextQuery:           query,
		IncludedType:        "restaurant",
		StrictTypeFiltering: true,
		OpenNow:             true,
		LocationBias: LocationBias{
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
	resp, err := c.doPost(ctx, endpoint, reqBody)
	if err != nil {
		return nil, err
	}

	// 應用層過濾：營業中與指定半徑過濾
	openPlaces := FilterOpenPlaces(resp.Places)
	filtered := FilterByRadius(openPlaces, lat, lng, radiusMeters)

	// 若第 1 頁候選經半徑過濾後為 0 筆且存在 nextPageToken，最多抓取第 2 頁，避免因 Google API 排序將遠處店家排於首頁導致誤判無結果
	const maxPages = 2
	currentPage := 1
	nextPageToken := resp.NextPageToken

	for len(filtered) == 0 && nextPageToken != "" && currentPage < maxPages {
		currentPage++
		pageReq := reqBody
		pageReq.PageToken = nextPageToken

		pageResp, pageErr := c.doPost(ctx, endpoint, pageReq)
		if pageErr != nil {
			return nil, fmt.Errorf("取得第 %d 頁餐廳失敗: %w", currentPage, pageErr)
		}

		pageOpen := FilterOpenPlaces(pageResp.Places)
		pageFiltered := FilterByRadius(pageOpen, lat, lng, radiusMeters)
		filtered = append(filtered, pageFiltered...)
		nextPageToken = pageResp.NextPageToken
	}

	return filtered, nil
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

func (c *placesClient) doPost(ctx context.Context, url string, payload any) (*PlacesResponse, error) {
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
		req.Header.Set("X-Goog-FieldMask", fieldMask)
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
