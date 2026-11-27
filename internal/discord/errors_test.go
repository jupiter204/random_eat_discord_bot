package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"random_eat_discord/internal/googlemaps"
	"random_eat_discord/internal/lottery"
)

func TestUserFacingMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"public sentinel", lottery.ErrNoPreference, lottery.ErrNoPreference.Error()},
		{"wrapped public sentinel", fmt.Errorf("外層: %w", lottery.ErrNoRestaurants), lottery.ErrNoRestaurants.Error()},
		{"timeout", fmt.Errorf("搜尋餐廳失敗: %w", context.DeadlineExceeded), "處理逾時，請稍後再試"},
		{"internal API error is hidden", errors.New(`Places API 回傳錯誤 (Status 403): {"error":{"message":"API key not valid"}}`), "系統暫時無法處理請求，請稍後再試"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := userFacingMessage(tc.err); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderDrawEmbed_EmptyAndOversizedValues(t *testing.T) {
	res := &lottery.DrawResult{
		SessionID: "s",
		Place: &googlemaps.Place{
			ID:          "p1",
			DisplayName: googlemaps.LocalizedText{Text: strings.Repeat("長", 400)},
		},
		QueryCtx: lottery.QueryContext{
			Radius:       1000,
			LocationName: "預設位置",
			Keyword:      strings.Repeat("字", 2000),
		},
	}

	embed, _ := RenderDrawEmbed(res)

	if n := utf8.RuneCountInString(embed.Title); n > maxTitleLen {
		t.Errorf("title length %d exceeds %d", n, maxTitleLen)
	}
	for _, f := range embed.Fields {
		if strings.TrimSpace(f.Value) == "" {
			t.Errorf("field %q has empty value, Discord would reject the embed", f.Name)
		}
		if n := utf8.RuneCountInString(f.Value); n > maxFieldValueLen {
			t.Errorf("field %q length %d exceeds %d", f.Name, n, maxFieldValueLen)
		}
	}
}

func TestRenderDrawEmbed_EmptyPlaceName(t *testing.T) {
	res := &lottery.DrawResult{
		Place:    &googlemaps.Place{ID: "p1"},
		QueryCtx: lottery.QueryContext{Radius: 1000},
	}
	embed, _ := RenderDrawEmbed(res)
	if embed.Title != "🍽️ 未命名餐廳" {
		t.Errorf("unexpected title for empty name: %q", embed.Title)
	}
}

func TestRenderErrorEmbed_TruncatesDescription(t *testing.T) {
	embed := RenderErrorEmbed("x", strings.Repeat("e", 5000))
	if n := utf8.RuneCountInString(embed.Description); n > maxDescriptionLen {
		t.Errorf("description length %d exceeds %d", n, maxDescriptionLen)
	}
}

func TestRenderCandidateListEmbed_TruncatesAndEscapes(t *testing.T) {
	var candidates []lottery.Candidate
	for i := 0; i < 200; i++ {
		candidates = append(candidates, lottery.Candidate{
			Place: googlemaps.Place{
				ID:            fmt.Sprintf("p%d", i),
				DisplayName:   googlemaps.LocalizedText{Text: "[分店] " + strings.Repeat("名", 30)},
				GoogleMapsURI: "https://maps.google.com/?cid=1234567890",
			},
			DistanceMeters: float64(i * 10),
		})
	}
	embed := RenderCandidateListEmbed(&lottery.ListResult{
		Candidates: candidates,
		QueryCtx:   lottery.QueryContext{Radius: 1000, LocationName: "預設位置"},
	})

	if n := utf8.RuneCountInString(embed.Description); n > maxDescriptionLen {
		t.Errorf("description length %d exceeds %d", n, maxDescriptionLen)
	}
	if !strings.Contains(embed.Description, "未顯示") {
		t.Errorf("expected hidden-count notice when list overflows")
	}
	if strings.Contains(embed.Description, "[分店]") {
		t.Errorf("square brackets in names should be escaped to keep markdown links intact")
	}
	if !strings.Contains(embed.Title, "共 200 家") {
		t.Errorf("title should show total count, got %q", embed.Title)
	}
}
