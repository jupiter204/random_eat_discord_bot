package googlemaps

import (
	"testing"
)

func boolPtr(b bool) *bool {
	return &b
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
