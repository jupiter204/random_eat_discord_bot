package googlemaps

import (
	"strings"
	"time"
)

// LocalizedText 表示 Google API 的多語系文字結構
type LocalizedText struct {
	Text         string `json:"text"`
	LanguageCode string `json:"languageCode,omitempty"`
}

// OpeningHours 代表 Google Places API 的營業時間結構
type OpeningHours struct {
	OpenNow             *bool    `json:"openNow,omitempty"`
	WeekdayDescriptions []string `json:"weekdayDescriptions,omitempty"`
	NextOpenTime        string   `json:"nextOpenTime,omitempty"`
	NextCloseTime       string   `json:"nextCloseTime,omitempty"`
}

// Place 代表 Places API (New) 回傳的餐廳資料結構
type Place struct {
	ID                  string        `json:"id"`
	DisplayName         LocalizedText `json:"displayName"`
	FormattedAddress    string        `json:"formattedAddress"`
	Rating              float64       `json:"rating,omitempty"`
	UserRatingCount     int           `json:"userRatingCount,omitempty"`
	GoogleMapsURI       string        `json:"googleMapsUri"`
	PriceLevel          string        `json:"priceLevel,omitempty"`
	CurrentOpeningHours *OpeningHours `json:"currentOpeningHours,omitempty"`
	RegularOpeningHours *OpeningHours `json:"regularOpeningHours,omitempty"`
}

// FormattedPriceLevel 將 Google 價位列舉轉換為簡潔易讀符號
func (p *Place) FormattedPriceLevel() string {
	switch p.PriceLevel {
	case "PRICE_LEVEL_FREE":
		return "免費"
	case "PRICE_LEVEL_INEXPENSIVE":
		return "$"
	case "PRICE_LEVEL_MODERATE":
		return "$$"
	case "PRICE_LEVEL_EXPENSIVE":
		return "$$$"
	case "PRICE_LEVEL_VERY_EXPENSIVE":
		return "$$$$"
	default:
		return "未提供"
	}
}

// IsOpenNow 判斷店家目前是否正在營業
func (p *Place) IsOpenNow() bool {
	if p.CurrentOpeningHours != nil && p.CurrentOpeningHours.OpenNow != nil {
		return *p.CurrentOpeningHours.OpenNow
	}
	if p.RegularOpeningHours != nil && p.RegularOpeningHours.OpenNow != nil {
		return *p.RegularOpeningHours.OpenNow
	}
	return false
}

// FilterOpenPlaces 過濾出只包含目前正在營業的店家清單
func FilterOpenPlaces(places []Place) []Place {
	openPlaces := make([]Place, 0, len(places))
	for _, p := range places {
		if p.IsOpenNow() {
			openPlaces = append(openPlaces, p)
		}
	}
	return openPlaces
}

// OpenStatusText 動態判斷並回傳當前營業狀態標籤
func (p *Place) OpenStatusText() string {
	var hours *OpeningHours
	if p.CurrentOpeningHours != nil {
		hours = p.CurrentOpeningHours
	} else if p.RegularOpeningHours != nil {
		hours = p.RegularOpeningHours
	}

	if hours == nil || hours.OpenNow == nil {
		return "營業狀態未知 ⚪"
	}

	if *hours.OpenNow {
		return "營業中 🟢"
	}
	return "休息中 🔴"
}

// TodayOpeningHours 取得今日的營業時間描述
func (p *Place) TodayOpeningHours() string {
	var hours *OpeningHours
	if p.CurrentOpeningHours != nil {
		hours = p.CurrentOpeningHours
	} else if p.RegularOpeningHours != nil {
		hours = p.RegularOpeningHours
	}

	if hours == nil || len(hours.WeekdayDescriptions) == 0 {
		return "未提供營業時間"
	}

	// 載入台北時區以對應今日星期
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	todayWeekday := time.Now().In(loc).Weekday()

	weekdayPrefixes := map[time.Weekday][]string{
		time.Sunday:    {"星期日", "週日", "Sunday"},
		time.Monday:    {"星期一", "週一", "Monday"},
		time.Tuesday:   {"星期二", "週二", "Tuesday"},
		time.Wednesday: {"星期三", "週三", "Wednesday"},
		time.Thursday:  {"星期四", "週四", "Thursday"},
		time.Friday:    {"星期五", "週五", "Friday"},
		time.Saturday:  {"星期六", "週六", "Saturday"},
	}

	prefixes := weekdayPrefixes[todayWeekday]
	for _, desc := range hours.WeekdayDescriptions {
		for _, prefix := range prefixes {
			if strings.HasPrefix(desc, prefix) {
				return desc
			}
		}
	}

	// 若未匹配前綴，回傳第一筆
	return hours.WeekdayDescriptions[0]
}

// LatLng 代表經緯度坐標
type LatLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Circle 代表地理圓形區域限制
type Circle struct {
	Center LatLng  `json:"center"`
	Radius float64 `json:"radius"`
}

// LocationRestriction 搜尋半徑限制結構（用於 searchNearby）
type LocationRestriction struct {
	Circle Circle `json:"circle"`
}

// LocationBias 搜尋偏好結構（用於 searchText）
type LocationBias struct {
	Circle Circle `json:"circle"`
}

// NearbySearchRequest Places API (New) searchNearby 請求
type NearbySearchRequest struct {
	IncludedTypes       []string            `json:"includedTypes"`
	MaxResultCount      int                 `json:"maxResultCount"`
	LocationRestriction LocationRestriction `json:"locationRestriction"`
}

// TextSearchRequest Places API (New) searchText 請求
type TextSearchRequest struct {
	TextQuery    string       `json:"textQuery"`
	IncludedType string       `json:"includedType,omitempty"`
	OpenNow      bool         `json:"openNow"`
	LocationBias LocationBias `json:"locationBias"`
}

// PlacesResponse Places API 統一回傳格式
type PlacesResponse struct {
	Places []Place `json:"places"`
}
