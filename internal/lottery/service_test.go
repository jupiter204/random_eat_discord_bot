package lottery

import (
	"context"
	"testing"
	"time"

	"random_eat_discord/internal/db"
	"random_eat_discord/internal/googlemaps"
)

type mockRepository struct {
	pref map[string]*db.UserPreference
}

func newMockRepo() *mockRepository {
	return &mockRepository{pref: make(map[string]*db.UserPreference)}
}

func (m *mockRepository) UpsertPreference(ctx context.Context, pref *db.UserPreference) error {
	m.pref[pref.UserID] = pref
	return nil
}

func (m *mockRepository) GetPreference(ctx context.Context, userID string) (*db.UserPreference, error) {
	p, ok := m.pref[userID]
	if !ok {
		return nil, nil
	}
	return p, nil
}

func (m *mockRepository) DeletePreference(ctx context.Context, userID string) error {
	delete(m.pref, userID)
	return nil
}

type mockMapsClient struct {
	places []googlemaps.Place
}

func (m *mockMapsClient) SearchNearbyRestaurants(ctx context.Context, lat, lng float64, radiusMeters int) ([]googlemaps.Place, error) {
	return m.places, nil
}

func (m *mockMapsClient) SearchTextRestaurants(ctx context.Context, query string, lat, lng float64, radiusMeters int) ([]googlemaps.Place, error) {
	return m.places, nil
}

func TestCoordinateValidation(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		lng     float64
		wantErr bool
	}{
		{"Valid Taipei 101", 25.0339, 121.5644, false},
		{"Invalid Lat High", 91.0, 121.5644, true},
		{"Invalid Lat Low", -90.5, 121.5644, true},
		{"Invalid Lng High", 25.0, 181.0, true},
		{"Invalid Lng Low", 25.0, -181.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCoordinates(tt.lat, tt.lng)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCoordinates(%v, %v) error = %v, wantErr %v", tt.lat, tt.lng, err, tt.wantErr)
			}
		})
	}
}

func TestSessionCacheReroll(t *testing.T) {
	cache := NewSessionCache()
	defer cache.Stop()

	places := []googlemaps.Place{
		{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "餐廳 A"}},
		{ID: "p2", DisplayName: googlemaps.LocalizedText{Text: "餐廳 B"}},
		{ID: "p3", DisplayName: googlemaps.LocalizedText{Text: "餐廳 C"}},
	}

	qCtx := QueryContext{
		Latitude:  25.0339,
		Longitude: 121.5644,
		Radius:    1000,
	}

	sessionID := "test-session-123"
	cache.Store(sessionID, places, qCtx, 5*time.Second)

	picked := make(map[string]bool)
	for i := 0; i < 3; i++ {
		p, remaining, _, ok := cache.PickNext(sessionID)
		if !ok {
			t.Fatalf("PickNext failed on iteration %d", i)
		}
		if picked[p.ID] {
			t.Fatalf("Duplicate place picked: %s", p.ID)
		}
		picked[p.ID] = true
		expectedRemaining := 3 - (i + 1)
		if remaining != expectedRemaining {
			t.Errorf("expected remaining %d, got %d", expectedRemaining, remaining)
		}
	}

	// 4th pick should cycle and still succeed
	p, remaining, _, ok := cache.PickNext(sessionID)
	if !ok || p == nil {
		t.Fatalf("expected cycle pick to succeed")
	}
	if remaining != 2 {
		t.Errorf("expected remaining after cycle reset to be 2, got %d", remaining)
	}
}

func TestLotteryServiceDrawAndFallback(t *testing.T) {
	repo := newMockRepo()
	mapsClient := &mockMapsClient{
		places: []googlemaps.Place{
			{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "測試餐廳"}},
		},
	}

	svc := NewService(repo, mapsClient)
	defer svc.Close()

	ctx := context.Background()

	// 1. 尚未設定偏好時呼叫 Draw (無即時坐標) -> 應回傳 ErrNoPreference
	_, err := svc.Draw(ctx, DrawRequest{UserID: "user123"})
	if err != ErrNoPreference {
		t.Fatalf("expected ErrNoPreference, got %v", err)
	}

	// 2. 設定使用者偏好
	err = svc.SetPreference(ctx, "user123", "公司", 25.0339, 121.5644, 1000)
	if err != nil {
		t.Fatalf("SetPreference failed: %v", err)
	}

	// 3. 再次抽籤 -> 成功取得偏好位置並完成抽籤
	res, err := svc.Draw(ctx, DrawRequest{UserID: "user123"})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	if res.Place.DisplayName.Text != "測試餐廳" {
		t.Errorf("expected 測試餐廳, got %s", res.Place.DisplayName.Text)
	}
	if res.QueryCtx.LocationName != "公司" {
		t.Errorf("expected location name 公司, got %s", res.QueryCtx.LocationName)
	}
}
