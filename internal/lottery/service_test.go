package lottery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
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

	// pages 設定時，SearchRestaurantsPage 依 token "p<N>" 回傳第 N 頁（0 起算），否則回傳 places 且無下一頁
	pages     [][]googlemaps.Place
	pageErr   map[int]error
	pageCalls int32
	tokens    []string
	keywords  []string
	mu        sync.Mutex
}

func (m *mockMapsClient) SearchRestaurantsPage(ctx context.Context, keyword string, lat, lng float64, radiusMeters int, pageToken string) ([]googlemaps.Place, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pageCalls++
	m.tokens = append(m.tokens, pageToken)
	m.keywords = append(m.keywords, keyword)

	if m.err != nil {
		return nil, "", m.err
	}
	if m.pages == nil {
		return m.places, "", nil
	}

	idx := 0
	if pageToken != "" {
		fmt.Sscanf(pageToken, "p%d", &idx)
	}
	if err := m.pageErr[idx]; err != nil {
		return nil, "", err
	}
	next := ""
	if idx+1 < len(m.pages) {
		next = fmt.Sprintf("p%d", idx+1)
	}
	return m.pages[idx], next, nil
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
	testNames := []string{"我家", "公司", "宿舍", "住家", "我家私密住址"}
	for _, name := range testNames {
		userID := "user-privacy-" + name
		_, _ = svc.SetPreference(ctx, userID, name, 25.0339, 121.5644, 1000)

		res, err := svc.Draw(ctx, DrawRequest{UserID: userID})
		if err != nil {
			t.Fatalf("Draw failed for %s: %v", name, err)
		}

		if res.QueryCtx.LocationName == name {
			t.Fatalf("CRITICAL: Private location name %s leaked in DrawResult.QueryCtx: %s", name, res.QueryCtx.LocationName)
		}
		if res.QueryCtx.LocationName != "預設位置" {
			t.Errorf("expected LocationName to be '預設位置', got '%s'", res.QueryCtx.LocationName)
		}
	}
}

func TestLotteryService_EmptyPlaces(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{places: []googlemaps.Place{}}
	svc := NewService(repo, client)
	defer svc.Close()

	ctx := context.Background()
	_, _ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

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
	_, _ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

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
	_, _ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

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
	_, _ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)

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

func TestSetPreference_ReturnsStoredValuesAndValidatesName(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, &mockMapsClient{})
	defer svc.Close()
	ctx := context.Background()

	pref, err := svc.SetPreference(ctx, "u1", "  公司  ", 25.0339, 121.5644, 0)
	if err != nil {
		t.Fatalf("SetPreference failed: %v", err)
	}
	if pref == nil || pref.Radius != 1000 || pref.LocationName != "公司" {
		t.Fatalf("unexpected returned preference: %+v", pref)
	}

	longName := strings.Repeat("家", MaxLocationNameLength+1)
	if _, err := svc.SetPreference(ctx, "u1", longName, 25.0339, 121.5644, 1000); err != ErrNameTooLong {
		t.Errorf("expected ErrNameTooLong, got %v", err)
	}
	okName := strings.Repeat("家", MaxLocationNameLength)
	if _, err := svc.SetPreference(ctx, "u1", okName, 25.0339, 121.5644, 1000); err != nil {
		t.Errorf("name at exact limit should be accepted, got %v", err)
	}
}

func TestDraw_KeywordTooLong(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, &mockMapsClient{places: []googlemaps.Place{{ID: "p1"}}})
	defer svc.Close()

	_, err := svc.Draw(context.Background(), DrawRequest{
		UserID:    "u1",
		Keyword:   strings.Repeat("麵", MaxKeywordLength+1),
		Latitude:  floatPtr(25.0339),
		Longitude: floatPtr(121.5644),
	})
	if err != ErrKeywordTooLong {
		t.Errorf("expected ErrKeywordTooLong, got %v", err)
	}
}

func TestDeletePreference(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, &mockMapsClient{places: []googlemaps.Place{{ID: "p1"}}})
	defer svc.Close()
	ctx := context.Background()

	_, _ = svc.SetPreference(ctx, "u1", "公司", 25.0339, 121.5644, 1000)
	if err := svc.DeletePreference(ctx, "u1"); err != nil {
		t.Fatalf("DeletePreference failed: %v", err)
	}
	if _, err := svc.Draw(ctx, DrawRequest{UserID: "u1"}); err != ErrNoPreference {
		t.Errorf("expected ErrNoPreference after delete, got %v", err)
	}
}

func TestList_SortedByDistanceWithSameValidation(t *testing.T) {
	repo := newMockRepo()
	client := &mockMapsClient{
		places: []googlemaps.Place{
			{ID: "far", Location: &googlemaps.LatLng{Latitude: 25.0400, Longitude: 121.5644}},
			{ID: "unknown"},
			{ID: "near", Location: &googlemaps.LatLng{Latitude: 25.0340, Longitude: 121.5644}},
		},
	}
	svc := NewService(repo, client)
	defer svc.Close()
	ctx := context.Background()

	res, err := svc.List(ctx, DrawRequest{UserID: "u1", Latitude: floatPtr(25.0339), Longitude: floatPtr(121.5644)})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	var order []string
	for _, c := range res.Candidates {
		order = append(order, c.Place.ID)
	}
	if strings.Join(order, ",") != "near,far,unknown" {
		t.Errorf("unexpected order: %v", order)
	}
	if res.Candidates[2].DistanceMeters != -1 {
		t.Errorf("expected -1 distance for place without location, got %v", res.Candidates[2].DistanceMeters)
	}

	// 與 Draw 共用驗證規則
	if _, err := svc.List(ctx, DrawRequest{UserID: "nobody"}); err != ErrNoPreference {
		t.Errorf("expected ErrNoPreference, got %v", err)
	}
}

func placesWithIDs(ids ...string) []googlemaps.Place {
	out := make([]googlemaps.Place, 0, len(ids))
	for _, id := range ids {
		out = append(out, googlemaps.Place{ID: id})
	}
	return out
}

func TestLazyPaging_DrawFetchesOnlyFirstPage(t *testing.T) {
	client := &mockMapsClient{pages: [][]googlemaps.Place{placesWithIDs("a", "b"), placesWithIDs("c")}}
	svc := NewService(newMockRepo(), client)
	defer svc.Close()

	res, err := svc.Draw(context.Background(), DrawRequest{UserID: "u1", Latitude: floatPtr(25.0), Longitude: floatPtr(121.5)})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	if client.pageCalls != 1 {
		t.Errorf("expected Draw to fetch only 1 page, got %d calls", client.pageCalls)
	}
	if !res.HasMore || res.TotalCount != 2 || res.RemainingCount != 1 {
		t.Errorf("unexpected result: HasMore=%v Total=%d Remaining=%d", res.HasMore, res.TotalCount, res.RemainingCount)
	}
}

func TestLazyPaging_RerollLoadsNextPageOnlyWhenExhausted(t *testing.T) {
	client := &mockMapsClient{pages: [][]googlemaps.Place{placesWithIDs("a", "b"), placesWithIDs("b", "c")}}
	svc := NewService(newMockRepo(), client)
	defer svc.Close()
	ctx := context.Background()

	res, err := svc.Draw(ctx, DrawRequest{UserID: "u1", Latitude: floatPtr(25.0), Longitude: floatPtr(121.5)})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	seen := map[string]bool{res.Place.ID: true}

	// 第 1 頁還剩 1 家：不應呼叫 API
	res, err = svc.Reroll(ctx, res.SessionID)
	if err != nil {
		t.Fatalf("Reroll 1 failed: %v", err)
	}
	seen[res.Place.ID] = true
	if client.pageCalls != 1 {
		t.Errorf("reroll within first page must not call API, got %d calls", client.pageCalls)
	}
	if res.RemainingCount != 0 || !res.HasMore {
		t.Errorf("expected first page exhausted with more pages, got Remaining=%d HasMore=%v", res.RemainingCount, res.HasMore)
	}

	// 第 1 頁抽完：載入第 2 頁（b 重複應被去除，只新增 c）
	res, err = svc.Reroll(ctx, res.SessionID)
	if err != nil {
		t.Fatalf("Reroll 2 failed: %v", err)
	}
	if client.pageCalls != 2 || client.tokens[1] != "p1" {
		t.Errorf("expected second page fetched with token p1, calls=%d tokens=%v", client.pageCalls, client.tokens)
	}
	if res.Place.ID != "c" || seen["c"] {
		t.Errorf("expected newly loaded place c, got %s", res.Place.ID)
	}
	if res.HasMore || res.RemainingCount != 0 {
		t.Errorf("expected no more pages, got HasMore=%v Remaining=%d", res.HasMore, res.RemainingCount)
	}

	// 全部抽完且沒有下一頁
	if _, err := svc.Reroll(ctx, res.SessionID); err != ErrNoMoreCandidates {
		t.Errorf("expected ErrNoMoreCandidates, got %v", err)
	}
	if client.pageCalls != 2 {
		t.Errorf("no extra API call expected after last page, got %d", client.pageCalls)
	}
}

func TestLazyPaging_DrawSkipsEmptyFirstPage(t *testing.T) {
	client := &mockMapsClient{pages: [][]googlemaps.Place{{}, placesWithIDs("a"), placesWithIDs("b")}}
	svc := NewService(newMockRepo(), client)
	defer svc.Close()

	res, err := svc.Draw(context.Background(), DrawRequest{UserID: "u1", Latitude: floatPtr(25.0), Longitude: floatPtr(121.5)})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	if res.Place.ID != "a" || client.pageCalls != 2 || !res.HasMore {
		t.Errorf("expected to skip empty page 1 and stop at page 2, got place=%s calls=%d hasMore=%v", res.Place.ID, client.pageCalls, res.HasMore)
	}
}

func TestLazyPaging_StopsAtMaxPages(t *testing.T) {
	client := &mockMapsClient{pages: [][]googlemaps.Place{{}, {}, {}, placesWithIDs("never")}}
	svc := NewService(newMockRepo(), client)
	defer svc.Close()

	_, err := svc.Draw(context.Background(), DrawRequest{UserID: "u1", Latitude: floatPtr(25.0), Longitude: floatPtr(121.5)})
	if err != ErrNoRestaurants {
		t.Errorf("expected ErrNoRestaurants after %d empty pages, got %v", maxSearchPages, err)
	}
	if client.pageCalls != maxSearchPages {
		t.Errorf("expected exactly %d page calls, got %d", maxSearchPages, client.pageCalls)
	}
}

func TestLazyPaging_FailedLoadCanBeRetried(t *testing.T) {
	boom := errors.New("network down")
	client := &mockMapsClient{
		pages:   [][]googlemaps.Place{placesWithIDs("a"), placesWithIDs("b")},
		pageErr: map[int]error{1: boom},
	}
	svc := NewService(newMockRepo(), client)
	defer svc.Close()
	ctx := context.Background()

	res, err := svc.Draw(ctx, DrawRequest{UserID: "u1", Latitude: floatPtr(25.0), Longitude: floatPtr(121.5)})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	if _, err := svc.Reroll(ctx, res.SessionID); !errors.Is(err, boom) {
		t.Fatalf("expected load error, got %v", err)
	}

	client.mu.Lock()
	delete(client.pageErr, 1)
	client.mu.Unlock()

	res2, err := svc.Reroll(ctx, res.SessionID)
	if err != nil || res2.Place.ID != "b" {
		t.Fatalf("expected retry to load page 2 and pick b, got %v, %v", res2, err)
	}
}

func TestLazyPaging_ConcurrentExhaustedRerollsFetchOnce(t *testing.T) {
	client := &mockMapsClient{pages: [][]googlemaps.Place{placesWithIDs("a"), placesWithIDs("b", "c", "d", "e")}}
	svc := NewService(newMockRepo(), client)
	defer svc.Close()
	ctx := context.Background()

	res, err := svc.Draw(ctx, DrawRequest{UserID: "u1", Latitude: floatPtr(25.0), Longitude: floatPtr(121.5)})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Reroll(ctx, res.SessionID)
			if err != nil && err != ErrLoadingMore && err != ErrNoMoreCandidates {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if client.pageCalls != 2 {
		t.Errorf("expected next page fetched exactly once, got %d total calls", client.pageCalls)
	}
}

func TestKeywordSearch_UsesLazyPagingWithSameKeyword(t *testing.T) {
	client := &mockMapsClient{pages: [][]googlemaps.Place{placesWithIDs("a"), placesWithIDs("b")}}
	svc := NewService(newMockRepo(), client)
	defer svc.Close()
	ctx := context.Background()

	res, err := svc.Draw(ctx, DrawRequest{UserID: "u1", Keyword: "拉麵", Latitude: floatPtr(25.0), Longitude: floatPtr(121.5)})
	if err != nil {
		t.Fatalf("Draw failed: %v", err)
	}
	if client.pageCalls != 1 || !res.HasMore {
		t.Fatalf("keyword search should load only page 1 and keep next page, calls=%d hasMore=%v", client.pageCalls, res.HasMore)
	}

	if _, err := svc.Reroll(ctx, res.SessionID); err != nil {
		t.Fatalf("Reroll failed: %v", err)
	}
	if fmt.Sprint(client.keywords) != fmt.Sprint([]string{"拉麵", "拉麵"}) {
		t.Errorf("next page must reuse the original keyword, got %v", client.keywords)
	}
}
