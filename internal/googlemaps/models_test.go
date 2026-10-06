package googlemaps

import (
	"math"
	"strings"
	"testing"
	"time"
)

func boolPtr(b bool) *bool {
	return &b
}

func intPtr(i int) *int {
	return &i
}

func TestPlace_IsOpenNow(t *testing.T) {
	tests := []struct {
		name     string
		place    Place
		expected bool
	}{
		{
			name: "CurrentOpeningHours Open",
			place: Place{
				CurrentOpeningHours: &OpeningHours{
					OpenNow: boolPtr(true),
				},
			},
			expected: true,
		},
		{
			name: "CurrentOpeningHours Closed",
			place: Place{
				CurrentOpeningHours: &OpeningHours{
					OpenNow: boolPtr(false),
				},
			},
			expected: false,
		},
		{
			name: "RegularOpeningHours Open fallback",
			place: Place{
				RegularOpeningHours: &OpeningHours{
					OpenNow: boolPtr(true),
				},
			},
			expected: true,
		},
		{
			name: "RegularOpeningHours Closed fallback",
			place: Place{
				RegularOpeningHours: &OpeningHours{
					OpenNow: boolPtr(false),
				},
			},
			expected: false,
		},
		{
			name:     "Nil hours",
			place:    Place{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.place.IsOpenNow(); got != tt.expected {
				t.Errorf("IsOpenNow() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestFilterOpenPlaces(t *testing.T) {
	places := []Place{
		{
			ID: "p1",
			CurrentOpeningHours: &OpeningHours{
				OpenNow: boolPtr(true),
			},
		},
		{
			ID: "p2",
			CurrentOpeningHours: &OpeningHours{
				OpenNow: boolPtr(false),
			},
		},
		{
			ID: "p3",
			RegularOpeningHours: &OpeningHours{
				OpenNow: boolPtr(true),
			},
		},
		{
			ID: "p4",
		},
	}

	filtered := FilterOpenPlaces(places)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 open places, got %d", len(filtered))
	}
	if filtered[0].ID != "p1" || filtered[1].ID != "p3" {
		t.Errorf("unexpected filtered places: %+v", filtered)
	}
}

func TestPlace_OpenStatusText(t *testing.T) {
	tests := []struct {
		name     string
		place    Place
		expected string
	}{
		{
			name: "CurrentOpeningHours Open",
			place: Place{
				CurrentOpeningHours: &OpeningHours{
					OpenNow: boolPtr(true),
				},
			},
			expected: "營業中 🟢",
		},
		{
			name: "CurrentOpeningHours Closed",
			place: Place{
				CurrentOpeningHours: &OpeningHours{
					OpenNow: boolPtr(false),
				},
			},
			expected: "休息中 🔴",
		},
		{
			name: "RegularOpeningHours Open fallback",
			place: Place{
				RegularOpeningHours: &OpeningHours{
					OpenNow: boolPtr(true),
				},
			},
			expected: "營業中 🟢",
		},
		{
			name:     "Nil hours",
			place:    Place{},
			expected: "營業狀態未知 ⚪",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.place.OpenStatusText()
			if got != tt.expected {
				t.Errorf("OpenStatusText() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestPlace_LocationTimezone(t *testing.T) {
	// 1. TimeZone ID 設定
	p1 := Place{TimeZone: &TimeZone{ID: "America/New_York"}}
	loc1 := p1.LocationTimezone()
	if loc1.String() != "America/New_York" {
		t.Errorf("expected America/New_York, got %s", loc1.String())
	}

	// 2. UTCOffsetMinutes 設定 (+540 = +9h Tokyo)
	p2 := Place{UTCOffsetMinutes: intPtr(540)}
	loc2 := p2.LocationTimezone()
	_, offset2 := time.Now().In(loc2).Zone()
	if offset2 != 540*60 {
		t.Errorf("expected offset %d, got %d", 540*60, offset2)
	}

	// 3. 兩者皆無時 fallback 到 time.Local
	p3 := Place{}
	loc3 := p3.LocationTimezone()
	if loc3 != time.Local {
		t.Errorf("expected time.Local fallback, got %v", loc3)
	}
}

func TestPlace_TodayOpeningHours(t *testing.T) {
	descriptions := []string{
		"星期日: 10:00 – 20:00",
		"星期一: 11:00 – 21:00",
		"星期二: 11:00 – 21:00",
		"星期三: 11:00 – 21:00",
		"星期四: 11:00 – 21:00",
		"星期五: 11:00 – 22:00",
		"星期六: 11:00 – 22:00",
	}

	p := Place{
		TimeZone: &TimeZone{ID: "Asia/Taipei"},
		CurrentOpeningHours: &OpeningHours{
			WeekdayDescriptions: descriptions,
		},
	}

	got := p.TodayOpeningHours()
	if got == "" || got == "未提供營業時間" {
		t.Errorf("expected opening hours, got: %s", got)
	}

	// 測試空資料
	emptyPlace := Place{}
	if gotEmpty := emptyPlace.TodayOpeningHours(); gotEmpty != "未提供營業時間" {
		t.Errorf("expected 未提供營業時間, got: %s", gotEmpty)
	}

	// 測試不同時區下的對應（例如跨越午夜的時區，確認 weekday 根據該時區計算）
	nyLoc, _ := time.LoadLocation("America/New_York")
	nyWeekday := time.Now().In(nyLoc).Weekday()
	nyPlace := Place{
		TimeZone: &TimeZone{ID: "America/New_York"},
		CurrentOpeningHours: &OpeningHours{
			WeekdayDescriptions: descriptions,
		},
	}
	nyHours := nyPlace.TodayOpeningHours()
	weekdayNames := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	expectedPrefix := weekdayNames[nyWeekday]
	if !strings.HasPrefix(nyHours, expectedPrefix) {
		t.Errorf("expected prefix %s in NY opening hours, got %s", expectedPrefix, nyHours)
	}
}

func TestHaversine(t *testing.T) {
	// 台北 101: 25.033976, 121.5645389
	// 台北車站: 25.047761, 121.517044
	// 地理直線大圓距離約 5.09 公里 (5000 ~ 5200 公尺)
	dist := Haversine(25.033976, 121.5645389, 25.047761, 121.517044)
	if dist < 5000 || dist > 5200 {
		t.Errorf("unexpected distance between Taipei 101 and Taipei Main Station: %.2f m", dist)
	}

	// 同一點距離應為 0
	sameDist := Haversine(25.033976, 121.5645389, 25.033976, 121.5645389)
	if math.Abs(sameDist) > 1e-6 {
		t.Errorf("expected 0 for same point, got %f", sameDist)
	}
}

func TestFilterByRadius(t *testing.T) {
	centerLat := 25.033976
	centerLng := 121.5645389 // 台北 101

	places := []Place{
		{
			ID:       "close", // 距離約 200m
			Location: &LatLng{Latitude: 25.0345, Longitude: 121.5650},
		},
		{
			ID:       "far", // 台北車站 (~5100m)
			Location: &LatLng{Latitude: 25.047761, Longitude: 121.517044},
		},
		{
			ID:       "no-location",
			Location: nil,
		},
	}

	// 搜尋半徑 1000m
	filtered := FilterByRadius(places, centerLat, centerLng, 1000)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 place within 1000m, got %d", len(filtered))
	}
	if filtered[0].ID != "close" {
		t.Errorf("expected close place, got %s", filtered[0].ID)
	}
}
