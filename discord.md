# Discord 餐廳抽籤機器人（Go 語言）指令與互動規格說明

本文檔詳細說明 `random_eat_discord` 專案之 Discord Slash Commands 規格、互動行為與後端處理機制。

---

## 1. Slash Commands 規格與參數定義

### 1.1 `/set` - 設定個人預設搜尋位置

設定使用者的預設經緯度坐標與搜尋半徑。後續使用 `/eat` 時若未帶入坐標，系統將自動套用此處儲存的個人偏好。

- **回應類型**：🔒 **Ephemeral（私密訊息，僅執行指令的使用者可見）**
- **保護機制**：防止使用者住家、辦公室等私人坐標在公共頻道中洩漏。
- **參數列表**：

| 參數名稱 | 類型 | 必填 | 範圍 / 限制 | 說明 |
| :--- | :--- | :--- | :--- | :--- |
| `latitude` | Number (Float) | **是** | -90.0 ~ 90.0 | 預設搜尋中心之緯度（例如：`25.0339`） |
| `longitude` | Number (Float) | **是** | -180.0 ~ 180.0 | 預設搜尋中心之經度（例如：`121.5644`） |
| `radius` | Integer | 否 | 100 ~ 5000 | 預設搜尋半徑（公尺），預設為 `1000` |
| `name` | String | 否 | 最多 50 字元 | 位置別名（例如：「公司」、「家」），僅於 `/set` 私密回應中顯示 |

---

### 1.2 `/eat` - 隨機抽取營業中餐廳

在指定範圍或個人預設範圍內隨機挑選一家營業中的餐廳，並支援零 API 費用的重抽互動按鈕。

- **回應類型**：📢 **公開頻道訊息**
- **隱私防護**：公開結果卡片絕不顯示使用者的私人別名（如「我家」），搜尋中心一律標記為「預設位置」或「指定坐標」。
- **參數列表**：

| 參數名稱 | 類型 | 必填 | 範圍 / 限制 | 說明 |
| :--- | :--- | :--- | :--- | :--- |
| `keyword` | String | 否 | 任何文字 | 指定食物品項或料理類型（例如：「拉麵」、「火鍋」、「素食」） |
| `latitude` | Number (Float) | 否 | -90.0 ~ 90.0 | 臨時指定搜尋緯度（必須與 `longitude` 成對提供） |
| `longitude` | Number (Float) | 否 | -180.0 ~ 180.0 | 臨時指定搜尋經度（必須與 `latitude` 成對提供） |
| `radius` | Integer | 否 | 100 ~ 5000 | 覆蓋預設搜尋半徑（公尺，留空則採用個人預設值或 1000m） |

> ⚠️ **坐標成對性規則**：
> - 若提供 `latitude`，則必須同時提供 `longitude`；
> - 若提供 `longitude`，則必須同時提供 `latitude`；
> - 若兩者皆未提供，則自動查詢資料庫中 `/set` 儲存的個人預設坐標；若資料庫無紀錄則回傳友善提示引導設定。

---

### 1.3 `/help` - 使用說明與坐標取得教學

提供機器人使用方式說明與手機/電腦 Google 地圖經緯度複製引導。

- **回應類型**：🔒 **Ephemeral（私密訊息）**
- **參數列表**：無參數

---

## 2. 互動與生命週期處理

### 2.1 3 秒逾時防禦 (Deferred Interaction)

Discord API 嚴格要求在 **3 秒** 內對 Interaction 請求給予回應。資料庫查詢、Google Places API (New) 請求與網路連線可能超過此時限。

- **實作方式**：
  1. 收到指令後，第一時間發送 `InteractionResponseDeferredChannelMessageWithSource`（顯示「思考中...」）。
  2. 抽籤邏輯完成後，呼叫 `InteractionResponseEdit` 更新為最終抽籤結果卡片。
  3. `/set` 則發送帶有 `MessageFlagsEphemeral` 標籤的 Deferred 回應，確保整個互動皆為私密。

### 2.2 零 API 額度重抽 (Reroll Button)

抽籤結果卡片下方附有 `[🔄 再抽一家 (不消耗 API)]` 互動按鈕。

- **CustomID**：`reroll:<session_id>`
- **權限語意**：採用**頻道共同決策**模式，任何能看見該抽籤訊息的頻道成員皆可點擊「再抽一家」按鈕參與決定。卡片更新後會在頁尾標示最新點擊發起者。
- **後端流程**：
  1. 點擊按鈕後立即發送 `InteractionResponseDeferredMessageUpdate`。
  2. 依據 `session_id` 從記憶體快取中挑選下一家**尚未被抽過**的餐廳（100% 記憶體運算，不花費 Google Places API 額度）。
  3. 更新原訊息之 Embed 卡片與剩餘備選計數。
- **終止與防重抽語意**：
  - 候選餐廳名單抽完後即終止，**不進行無限循環重複**，以避免反覆抽到已否決的店家。
  - 當剩餘備選為 0 家時，按鈕自動標記為 `Disabled: true` 並顯示「已無更多備選餐廳」。
  - 若已過期或無更多候選，系統透過 Ephemeral 提示「沒有更多候選餐廳，請重新使用 /eat」，維護頻道整潔。

---

## 3. Google Places API (New) 整合邏輯

- **無關鍵字搜尋**：
  - 呼叫端點：`POST /v1/places:searchNearby`
  - 參數設定：`includedTypes: ["restaurant"]`, `openNow: true`, `locationRestriction: { circle: { center, radius } }`
- **關鍵字搜尋**：
  - 呼叫端點：`POST /v1/places:searchText`
  - 參數設定：`includedType: "restaurant"`, `strictTypeFiltering: true`, `openNow: true`, `locationBias: { circle: { center, radius } }`
- **後端驗證與雙重過濾**：
  - 距離保證：所有候選經由後端 Haversine 公式計算距離，保證不超出搜尋半徑。
  - 營業中保證：僅保留明確標示為營業中之店家（排除未知與打烊）。
  - 時區解析：依據店家回傳時區動態計算今日營業時間描述。