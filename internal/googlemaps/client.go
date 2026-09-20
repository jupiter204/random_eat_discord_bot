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
	nearbySearchURL = "https://places.googleapis.com/v1/places:searchNearby"
	textSearchURL   = "https://places.googleapis.com/v1/places:searchText"
	fieldMask       = "places.id,places.displayName,places.formattedAddress,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel,places.currentOpeningHours,places.regularOpeningHours"
)

// Client 定義 Google Places API 用戶端介面
type Client interface {
	SearchNearbyRestaurants(ctx context.Context, lat, lng float64, radiusMeters int) ([]Place, error)
	SearchTextRestaurants(ctx context.Context, query string, lat, lng float64, radiusMeters int) ([]Place, error)
}

type placesClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient 建立 Google Places API 客戶端實例
func NewClient(apiKey string) Client {
	return &placesClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SearchNearbyRestaurants 搜尋指定坐標與半徑內營業中的餐廳
func (c *placesClient) SearchNearbyRestaurants(ctx context.Context, lat, lng float64, radiusMeters int) ([]Place, error) {
	reqBody := NearbySearchRequest{
		IncludedTypes:  []string{"restaurant"},
		OpenNow:        true,
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

	return c.doPost(ctx, nearbySearchURL, reqBody)
}

// SearchTextRestaurants 搜尋指定關鍵字且位於該坐標偏好範圍內的餐廳
func (c *placesClient) SearchTextRestaurants(ctx context.Context, query string, lat, lng float64, radiusMeters int) ([]Place, error) {
	reqBody := TextSearchRequest{
		TextQuery: query,
		OpenNow:   true,
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

	return c.doPost(ctx, textSearchURL, reqBody)
}

func (c *placesClient) doPost(ctx context.Context, url string, payload any) ([]Place, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化 API 請求失敗: %w", err)
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
		return nil, fmt.Errorf("呼叫 Places API 失敗: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("讀取 Places API 回應失敗: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Places API 回傳錯誤 (Status %d): %s", resp.StatusCode, string(respBody))
	}

	var placesResp PlacesResponse
	if err := json.Unmarshal(respBody, &placesResp); err != nil {
		return nil, fmt.Errorf("解析 Places API 回應失敗: %w", err)
	}

	return placesResp.Places, nil
}
