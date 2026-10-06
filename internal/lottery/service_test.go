package lottery

import (
	"context"
	"math"
	"sync"
	"testing"

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
	err    error
}

func (m *mockMapsClient) SearchNearbyRestaurants(ctx context.Context, lat, lng float64, radiusMeters int) ([]googlemaps.Place, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.places, nil
}

func (m *mockMapsClient) SearchTextRestaurants(ctx context.Context, query string, lat, lng float64, radiusMeters int) ([]googlemaps.Place, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.places, nil
}

func floatPtr(f float64) *float64 {
	return &f
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
		{"Invalid Lat NaN", math.NaN(), 121.5644, true},
		{"Invalid Lng NaN", 25.0339, math.NaN(), true},
		{"Invalid Lat +Inf", math.Inf(1), 121.5644, true},
		{"Invalid Lat -Inf", math.Inf(-1), 121.5644, true},
		{"Invalid Lng +Inf", 25.0339, math.Inf(1), true},
		{"Invalid Lng -Inf", 25.0339, math.Inf(-1), true},
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

func TestCoordinatePairRequirement(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()

	// 1. 僅提供 latitude，未提供 longitude
	_, err := svc.Draw(ctx, DrawRequest{
		UserID:   "u1",
		Latitude: floatPtr(25.0339),
	})
	if err != ErrCoordinatePairRequired {
		t.Errorf("expected ErrCoordinatePairRequired when only lat provided, got %v", err)
	}

	// 2. 僅提供 longitude，未提供 latitude
	_, err = svc.Draw(ctx, DrawRequest{
		UserID:    "u1",
		Longitude: floatPtr(121.5644),
	})
	if err != ErrCoordinatePairRequired {
		t.Errorf("expected ErrCoordinatePairRequired when only lng provided, got %v", err)
	}
}

func TestPrivacy_LocationNameNeverExposed(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{
		places: []googlemaps.Place{
			{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "巷口麵店"}},
		},
	}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	// 使用者將位置別名設定為「我家私密住址」
	_ = svc.SetPreference(ctx, "user-privacy", "我家私密住址", 25.0339, 121.5644, 1000)

	// 使用預設偏好抽籤
	res, err := svc.Draw(ctx, DrawRequest{UserID: "user-privacy"})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}

	// 公開抽籤結果之 LocationName 絕對不得出現使用者自訂的「我家私密住址」
	if res.QueryCtx.LocationName == "我家私密住址" {
		t.Fatalf("CRITICAL: Private location name leaked in DrawResult.QueryCtx: %s", res.QueryCtx.LocationName)
	}
	if res.QueryCtx.LocationName != "預設位置" {
		t.Errorf("expected LocationName to be '預設位置', got '%s'", res.QueryCtx.LocationName)
	}
}

func TestLotteryService_EmptyPlaces(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{places: []googlemaps.Place{}}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	_ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

	_, err := svc.Draw(ctx, DrawRequest{UserID: "u1"})
	if err != ErrNoRestaurants {
		t.Fatalf("expected ErrNoRestaurants for empty places, got %v", err)
	}
}

func TestLotteryService_OnePlaceAndExhaustion(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{
		places: []googlemaps.Place{
			{ID: "single-1", DisplayName: googlemaps.LocalizedText{Text: "唯一餐廳"}},
		},
	}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	_ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

	res, err := svc.Draw(ctx, DrawRequest{UserID: "u1"})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	if res.Place.ID != "single-1" {
		t.Errorf("expected single-1, got %s", res.Place.ID)
	}
	if res.RemainingCount != 0 {
		t.Errorf("expected 0 remaining, got %d", res.RemainingCount)
	}

	// 嘗試 Reroll，此時候選已空，應回傳 ErrNoMoreCandidates
	_, err = svc.Reroll(ctx, res.SessionID)
	if err != ErrNoMoreCandidates {
		t.Fatalf("expected ErrNoMoreCandidates on exhausted session, got %v", err)
	}
}

func TestLotteryService_MultiplePlacesAndRerollSequence(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{
		places: []googlemaps.Place{
			{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "餐廳 1"}},
			{ID: "p2", DisplayName: googlemaps.LocalizedText{Text: "餐廳 2"}},
			{ID: "p3", DisplayName: googlemaps.LocalizedText{Text: "餐廳 3"}},
		},
	}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	_ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

	res1, err := svc.Draw(ctx, DrawRequest{UserID: "u1"})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	if res1.RemainingCount != 2 {
		t.Errorf("expected remaining 2, got %d", res1.RemainingCount)
	}

	res2, err := svc.Reroll(ctx, res1.SessionID)
	if err != nil {
		t.Fatalf("Reroll 1 failed: %v", err)
	}
	if res2.Place.ID == res1.Place.ID {
		t.Errorf("Reroll returned duplicate place: %s", res2.Place.ID)
	}
	if res2.RemainingCount != 1 {
		t.Errorf("expected remaining 1, got %d", res2.RemainingCount)
	}

	res3, err := svc.Reroll(ctx, res1.SessionID)
	if err != nil {
		t.Fatalf("Reroll 2 failed: %v", err)
	}
	if res3.Place.ID == res1.Place.ID || res3.Place.ID == res2.Place.ID {
		t.Errorf("Reroll returned duplicate place: %s", res3.Place.ID)
	}
	if res3.RemainingCount != 0 {
		t.Errorf("expected remaining 0, got %d", res3.RemainingCount)
	}

	// 第 4 次 Reroll: 候選全部用罄
	_, err = svc.Reroll(ctx, res1.SessionID)
	if err != ErrNoMoreCandidates {
		t.Errorf("expected ErrNoMoreCandidates, got %v", err)
	}
}

func TestLotteryService_DuplicatePlaceIDs(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{
		places: []googlemaps.Place{
			{ID: "dup-1", DisplayName: googlemaps.LocalizedText{Text: "重複餐廳 1"}},
			{ID: "dup-1", DisplayName: googlemaps.LocalizedText{Text: "重複餐廳 1 複本"}},
			{ID: "p2", DisplayName: googlemaps.LocalizedText{Text: "餐廳 2"}},
		},
	}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	_ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

	res, err := svc.Draw(ctx, DrawRequest{UserID: "u1"})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}

	// 確保後續抽取仍然正常終止，不會死循環
	for i := 0; i < 5; i++ {
		_, _ = svc.Reroll(ctx, res.SessionID)
	}
}

func TestLotteryService_ExpiredSession(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{
		places: []googlemaps.Place{
			{ID: "p1", DisplayName: googlemaps.LocalizedText{Text: "餐廳 1"}},
		},
	}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	_, err := svc.Reroll(ctx, "non-existent-session-id")
	if err != ErrSessionExpired {
		t.Errorf("expected ErrSessionExpired for non-existent session, got %v", err)
	}
}

func TestLotteryService_ConcurrentPickNext(t *testing.T) {
	repo := newMockRepo()
	var places []googlemaps.Place
	for i := 0; i < 50; i++ {
		places = append(places, googlemaps.Place{
			ID:          string(rune('A' + i)),
			DisplayName: googlemaps.LocalizedText{Text: "餐廳"},
		})
	}
	client := &mockMapsClient{places: places}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	res, err := svc.Draw(ctx, DrawRequest{
		Latitude:  floatPtr(25.0339),
		Longitude: floatPtr(121.5644),
	})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}

	var wg sync.WaitGroup
	const workers = 30
	wg.Add(workers)

	pickedIDs := sync.Map{}
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			r, err := svc.Reroll(ctx, res.SessionID)
			if err == nil && r != nil && r.Place != nil {
				if _, loaded := pickedIDs.LoadOrStore(r.Place.ID, true); loaded {
					t.Errorf("duplicate place picked in concurrent reroll: %s", r.Place.ID)
				}
			}
		}()
	}

	wg.Wait()
}
