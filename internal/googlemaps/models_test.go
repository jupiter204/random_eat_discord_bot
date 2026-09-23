package googlemaps

import (
	"testing"
)

func boolPtr(b bool) *bool {
	return &b
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

func TestPlace_TodayOpeningHours(t *testing.T) {
	placeWithDescriptions := Place{
		CurrentOpeningHours: &OpeningHours{
			WeekdayDescriptions: []string{
				"星期一: 11:00 – 21:00",
				"星期二: 11:00 – 21:00",
				"星期三: 11:00 – 21:00",
				"星期四: 11:00 – 21:00",
				"星期五: 11:00 – 22:00",
				"星期六: 11:00 – 22:00",
				"星期日: 11:00 – 21:00",
			},
		},
	}

	got := placeWithDescriptions.TodayOpeningHours()
	if got == "" || got == "未提供營業時間" {
		t.Errorf("expected today's opening hours, got: %s", got)
	}

	placeEmpty := Place{}
	if gotEmpty := placeEmpty.TodayOpeningHours(); gotEmpty != "未提供營業時間" {
		t.Errorf("expected 未提供營業時間, got: %s", gotEmpty)
	}
}
