package googlemaps

// LocalizedText 表示 Google API 的多語系文字結構
type LocalizedText struct {
	Text         string `json:"text"`
	LanguageCode string `json:"languageCode,omitempty"`
}

// Place 代表 Places API (New) 回傳的餐廳資料結構
type Place struct {
	ID               string        `json:"id"`
	DisplayName      LocalizedText `json:"displayName"`
	FormattedAddress string        `json:"formattedAddress"`
	Rating           float64       `json:"rating,omitempty"`
	UserRatingCount  int           `json:"userRatingCount,omitempty"`
	GoogleMapsURI    string        `json:"googleMapsUri"`
	PriceLevel       string        `json:"priceLevel,omitempty"`
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
	OpenNow             bool                `json:"openNow"`
	MaxResultCount      int                 `json:"maxResultCount"`
	LocationRestriction LocationRestriction `json:"locationRestriction"`
}

// TextSearchRequest Places API (New) searchText 請求
type TextSearchRequest struct {
	TextQuery    string       `json:"textQuery"`
	OpenNow      bool         `json:"openNow"`
	LocationBias LocationBias `json:"locationBias"`
}

// PlacesResponse Places API 統一回傳格式
type PlacesResponse struct {
	Places []Place `json:"places"`
}
