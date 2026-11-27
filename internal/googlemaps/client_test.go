package googlemaps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_SearchTextRestaurants_ContractAndPagination(t *testing.T) {
	var requestCount int32
	var capturedFieldMask string
	var page1Req TextSearchRequest
	var page2Req TextSearchRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		capturedFieldMask = r.Header.Get("X-Goog-FieldMask")

		if count == 1 {
			_ = json.NewDecoder(r.Body).Decode(&page1Req)
			// 第 1 頁：回傳 1 間距離過遠 (>1000m) 的餐廳，並附帶 nextPageToken
			resp := PlacesResponse{
				Places: []Place{
					{
						ID:                  "place-page1-far",
						DisplayName:         LocalizedText{Text: "遠方拉麵"},
						Location:            &LatLng{Latitude: 25.1000, Longitude: 121.5645}, // 遠超 1000m
						CurrentOpeningHours: &OpeningHours{OpenNow: boolPtr(true)},
					},
				},
				NextPageToken: "token-page-2",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if count == 2 {
			_ = json.NewDecoder(r.Body).Decode(&page2Req)
			// 第 2 頁：回傳 1 間距離近 (<1000m) 的餐廳
			resp := PlacesResponse{
				Places: []Place{
					{
						ID:                  "place-page2-close",
						DisplayName:         LocalizedText{Text: "近處拉麵"},
						Location:            &LatLng{Latitude: 25.0340, Longitude: 121.5645}, // 距離極近 (~10m)
						CurrentOpeningHours: &OpeningHours{OpenNow: boolPtr(true)},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
	}))
	defer server.Close()

	client := NewTestClient("test-key-456", server.URL, 2, 10*time.Millisecond, nil)

	places, err := client.SearchTextRestaurants(context.Background(), "拉麵", 25.0339, 121.5644, 1000)
	if err != nil {
		t.Fatalf("SearchTextRestaurants failed: %v", err)
	}

	// 驗證 Field Mask 必須包含 nextPageToken
	if !strings.Contains(capturedFieldMask, "nextPageToken") {
		t.Errorf("expected field mask to include nextPageToken, got: %s", capturedFieldMask)
	}

	// 驗證第 1 頁合約
	if page1Req.TextQuery != "拉麵" {
		t.Errorf("expected textQuery '拉麵', got %s", page1Req.TextQuery)
	}
	if page1Req.IncludedType != "restaurant" {
		t.Errorf("expected includedType 'restaurant', got %s", page1Req.IncludedType)
	}
	if !page1Req.StrictTypeFiltering {
		t.Errorf("expected StrictTypeFiltering true")
	}
	if !page1Req.OpenNow {
		t.Errorf("expected OpenNow true in TextSearchRequest")
	}

	// 驗證分頁：由於第 1 頁在 1000m 內為 0 筆，client 應自動查詢第 2 頁
	if atomic.LoadInt32(&requestCount) != 2 {
		t.Errorf("expected 2 requests for pagination, got %d", requestCount)
	}
	if page2Req.PageToken != "token-page-2" {
		t.Errorf("expected page2 pageToken 'token-page-2', got %s", page2Req.PageToken)
	}

	// 驗證最終結果為第 2 頁篩選出的餐廳
	if len(places) != 1 || places[0].ID != "place-page2-close" {
		t.Fatalf("expected place-page2-close, got %+v", places)
	}
}

func TestClient_SearchTextRestaurants_Page2ErrorNotSwallowed(t *testing.T) {
	var requestCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)

		if count == 1 {
			// 第 1 頁：無有效結果且提供 nextPageToken
			resp := PlacesResponse{
				Places: []Place{
					{
						ID:                  "place-far",
						Location:            &LatLng{Latitude: 25.1000, Longitude: 121.5645},
						CurrentOpeningHours: &OpeningHours{OpenNow: boolPtr(true)},
					},
				},
				NextPageToken: "token-page-2",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// 第 2 頁：發生 API 錯誤 (例如 500)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": "page 2 failed"}`))
	}))
	defer server.Close()

	// 設定 maxRetries: 0 以便立即驗證錯誤回傳
	client := NewTestClient("test-key", server.URL, 0, 10*time.Millisecond, nil)
	places, err := client.SearchTextRestaurants(context.Background(), "拉麵", 25.0339, 121.5644, 1000)

	// 驗證第 2 頁錯誤絕不被吞掉，且不得回傳 nil error 與空結果
	if err == nil {
		t.Fatalf("expected error when page 2 fails, got nil (places: %+v)", places)
	}
	if !strings.Contains(err.Error(), "取得第 2 頁餐廳失敗") && !strings.Contains(err.Error(), "500") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestClient_NonRetryableErrors(t *testing.T) {
	errorCodes := []int{
		http.StatusBadRequest,   // 400
		http.StatusUnauthorized, // 401
		http.StatusForbidden,    // 403
		http.StatusNotFound,     // 404
	}

	for _, statusCode := range errorCodes {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requestCount, 1)
				w.WriteHeader(statusCode)
				_, _ = w.Write([]byte(`{"error": "client error"}`))
			}))
			defer server.Close()

			client := NewTestClient("test-key", server.URL, 2, 10*time.Millisecond, nil)
			_, _, err := client.SearchOpenRestaurantsPage(context.Background(), 25.0, 121.0, 1000, "")
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", statusCode)
			}

			// 非暫態錯誤絕不可重試，請求數必須為 1
			if count := atomic.LoadInt32(&requestCount); count != 1 {
				t.Errorf("expected exactly 1 request for status %d, got %d", statusCode, count)
			}
		})
	}
}

func TestClient_RetryTransientErrors(t *testing.T) {
	transientCodes := []int{
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
	}

	for _, statusCode := range transientCodes {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := atomic.AddInt32(&requestCount, 1)
				if count < 3 {
					w.WriteHeader(statusCode)
					_, _ = w.Write([]byte(`{"error": "transient error"}`))
					return
				}
				// 第 3 次重試成功
				resp := PlacesResponse{
					Places: []Place{
						{
							ID:                  "recovered-place",
							Types:               []string{"restaurant"},
							DisplayName:         LocalizedText{Text: "重試成功餐廳"},
							Location:            &LatLng{Latitude: 25.0, Longitude: 121.0},
							CurrentOpeningHours: &OpeningHours{OpenNow: boolPtr(true)},
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			// 設定最多重試 2 次 (總共 3 次請求)
			client := NewTestClient("test-key", server.URL, 2, 10*time.Millisecond, nil)
			places, _, err := client.SearchOpenRestaurantsPage(context.Background(), 25.0, 121.0, 1000, "")
			if err != nil {
				t.Fatalf("expected success after retries for status %d, got error: %v", statusCode, err)
			}

			if count := atomic.LoadInt32(&requestCount); count != 3 {
				t.Errorf("expected 3 requests for status %d, got %d", statusCode, count)
			}
			if len(places) != 1 || places[0].ID != "recovered-place" {
				t.Errorf("unexpected places returned: %+v", places)
			}
		})
	}
}

func TestClient_NetworkTimeoutAndCancellation(t *testing.T) {
	// 模擬逾時
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	client := NewTestClient("test-key", server.URL, 2, 10*time.Millisecond, nil)
	_, _, err := client.SearchOpenRestaurantsPage(ctx, 25.0, 121.0, 1000, "")
	if err == nil {
		t.Fatalf("expected context timeout error, got nil")
	}
}

func TestClient_InvalidJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not-valid-json`))
	}))
	defer server.Close()

	client := NewTestClient("test-key", server.URL, 0, 10*time.Millisecond, nil)
	_, _, err := client.SearchOpenRestaurantsPage(context.Background(), 25.0, 121.0, 1000, "")
	if err == nil || !strings.Contains(err.Error(), "解析 Places API 回應失敗") {
		t.Fatalf("expected json parse error, got: %v", err)
	}
}

func TestClient_SearchOpenRestaurantsPage_ContractAndFilters(t *testing.T) {
	var reqs []TextSearchRequest
	var rawBodies []map[string]any
	var fieldMask string

	near := func(id string, types ...string) Place {
		return Place{
			ID:                  id,
			Types:               types,
			Location:            &LatLng{Latitude: 25.0340, Longitude: 121.5645},
			CurrentOpeningHours: &OpeningHours{OpenNow: boolPtr(true)},
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fieldMask = r.Header.Get("X-Goog-FieldMask")
		body, _ := io.ReadAll(r.Body)
		var req TextSearchRequest
		_ = json.Unmarshal(body, &req)
		var raw map[string]any
		_ = json.Unmarshal(body, &raw)
		reqs = append(reqs, req)
		rawBodies = append(rawBodies, raw)

		far := Place{
			ID: "far", Types: []string{"restaurant"},
			Location:            &LatLng{Latitude: 25.1000, Longitude: 121.5645},
			CurrentOpeningHours: &OpeningHours{OpenNow: boolPtr(true)},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PlacesResponse{
			Places:        []Place{near("a", "restaurant"), near("hotel", "lodging"), near("b", "ramen_restaurant"), far},
			NextPageToken: "t2",
		})
	}))
	defer server.Close()

	client := NewTestClient("k", server.URL, 0, time.Millisecond, nil)

	// 第 1 頁：只發 1 次請求，回傳過濾後的店家與下一頁 token
	places, next, err := client.SearchOpenRestaurantsPage(context.Background(), 25.0339, 121.5644, 1000, "")
	if err != nil {
		t.Fatalf("SearchOpenRestaurantsPage failed: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected exactly 1 request per page, got %d", len(reqs))
	}
	if next != "t2" {
		t.Errorf("expected next page token t2, got %q", next)
	}
	var ids []string
	for _, p := range places {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "a,b" {
		t.Errorf("expected food places within radius [a b], got %v", ids)
	}

	first := reqs[0]
	if first.TextQuery != DefaultTextQuery || !first.OpenNow || first.PageSize != 20 || first.PageToken != "" {
		t.Errorf("unexpected request contract: %+v", first)
	}
	if first.LocationRestriction == nil {
		t.Fatalf("expected rectangle locationRestriction")
	}
	rect := first.LocationRestriction.Rectangle
	if !(rect.Low.Latitude < 25.0339 && rect.High.Latitude > 25.0339 && rect.Low.Longitude < 121.5644 && rect.High.Longitude > 121.5644) {
		t.Errorf("rectangle does not contain center: %+v", rect)
	}
	if _, ok := rawBodies[0]["locationBias"]; ok {
		t.Errorf("locationBias must be omitted when locationRestriction is used")
	}
	if !strings.Contains(fieldMask, "places.types") || !strings.Contains(fieldMask, "nextPageToken") {
		t.Errorf("field mask missing types or nextPageToken: %s", fieldMask)
	}

	// 下一頁：必須帶 pageToken，且其餘參數與第 1 頁完全相同
	if _, _, err := client.SearchOpenRestaurantsPage(context.Background(), 25.0339, 121.5644, 1000, "t2"); err != nil {
		t.Fatalf("page 2 failed: %v", err)
	}
	second := reqs[1]
	if second.PageToken != "t2" {
		t.Errorf("expected pageToken t2, got %q", second.PageToken)
	}
	second.PageToken = ""
	if fmt.Sprintf("%+v", *second.LocationRestriction) != fmt.Sprintf("%+v", *first.LocationRestriction) ||
		second.TextQuery != first.TextQuery || second.OpenNow != first.OpenNow || second.PageSize != first.PageSize {
		t.Errorf("page 2 parameters must match page 1: %+v vs %+v", second, first)
	}
}

func TestBoundingRectangle_CoversRadius(t *testing.T) {
	lat, lng, r := 25.0339, 121.5644, 1000
	rect := BoundingRectangle(lat, lng, r)
	// 圓的正東、正北邊界點必須落在矩形內，且矩形邊緣距中心不應遠超半徑
	if d := Haversine(lat, lng, rect.High.Latitude, lng); d < float64(r) || d > float64(r)*1.05 {
		t.Errorf("north edge distance %.1f not within [r, 1.05r]", d)
	}
	if d := Haversine(lat, lng, lat, rect.High.Longitude); d < float64(r) || d > float64(r)*1.05 {
		t.Errorf("east edge distance %.1f not within [r, 1.05r]", d)
	}
}
