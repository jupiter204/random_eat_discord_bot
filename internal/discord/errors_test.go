package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

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

func TestRenderDrawEmbed_RerollButtonWithMorePages(t *testing.T) {
	button := func(remaining int, hasMore bool) discordgo.Button {
		_, comps := RenderDrawEmbed(&lottery.DrawResult{
			SessionID:      "s",
			Place:          &googlemaps.Place{ID: "p"},
			RemainingCount: remaining,
			HasMore:        hasMore,
			QueryCtx:       lottery.QueryContext{Radius: 1000},
		})
		return comps[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	}

	if b := button(0, true); b.Disabled || !strings.Contains(b.Label, "載入更多") {
		t.Errorf("exhausted page with more pages: expected enabled load-more button, got %+v", b)
	}
	if b := button(0, false); !b.Disabled {
		t.Errorf("no candidates and no more pages: expected disabled button")
	}
	if b := button(3, true); b.Disabled || strings.Contains(b.Label, "載入更多") {
		t.Errorf("candidates remaining: expected normal button, got %+v", b)
	}
}

func TestCanUseEatList(t *testing.T) {
	h := NewHandler(nil, []string{"allowed-user"})
	if !h.canUseEatList("allowed-user") {
		t.Errorf("allowed user should be permitted")
	}
	if h.canUseEatList("someone-else") || h.canUseEatList("") {
		t.Errorf("non-allowed or empty user must be rejected")
	}
	if NewHandler(nil, nil).canUseEatList("allowed-user") {
		t.Errorf("empty allowlist must reject everyone")
	}
}

func TestCommandsToRegister(t *testing.T) {
	has := func(cmds []*discordgo.ApplicationCommand, name string) bool {
		for _, c := range cmds {
			if c.Name == name {
				return true
			}
		}
		return false
	}
	if has(commandsToRegister(false), eatListCommandName) {
		t.Errorf("eat-list must not be registered without an allowlist")
	}
	enabled := commandsToRegister(true)
	if !has(enabled, eatListCommandName) || len(enabled) != len(SlashCommands) {
		t.Errorf("all commands including eat-list should be registered when enabled")
	}
	for _, name := range []string{"set", "eat", "unset", "help"} {
		if !has(commandsToRegister(false), name) {
			t.Errorf("command %s must always be registered", name)
		}
	}
}
