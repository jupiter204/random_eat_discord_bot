# Discord 餐廳抽籤機器人（Go 語言）系統設計與實作規格書 (SPEC)

## 1. 專案概述

本專案為一個基於 **Go 語言** 開發的 Discord 餐廳抽籤機器人。使用者**直接輸入經緯度坐標**或使用儲存的個人預設坐標，隨機抽取附近營業中的餐廳。支援自訂搜尋半徑、餐點關鍵字搜尋，並提供免重複扣 Google API 額度的「再抽一次（Reroll）」互動按鈕。

### 核心設計理念

- **零 Geocoding 費用**：由使用者直接提供精確經緯度坐標（例如：Google 地圖長按複製的 `25.0339, 121.5644`），免去地址解析與 Geocoding API 費用及延遲。
- **極輕量與低資源需求**：採用純 Go SQLite 驅動與單連線快取限制最佳化，無大型外部依賴，具備低資源需求特性。
- **單次查詢與零 API 浪費重抽機制**：批次取得候選餐廳並暫存於記憶體（10 分鐘 TTL），按鈕重抽完全由記憶體挑選，不重新呼叫 Google API。
- **嚴格過濾與品質保證**：嚴格限制餐廳類別、嚴格 Haversine 半徑限制、明確營業狀態過濾與所在時區動態解析。
- **客觀隨機抽選**：隨機選擇範圍為 Google Places API 回傳並通過條件過濾後的候選集合，而非保證掌握半徑內所有餐廳之全局清單。

### 核心技術選型

- **程式語言**：Go 1.24+
- **Discord SDK**：`github.com/bwmarrin/discordgo`
- **資料庫**：純 Go SQLite 驅動 `modernc.org/sqlite`（無須 CGO）
- **地理資訊服務**：Google Places API (New)（`searchText`）

---

## 2. 系統架構與工作流程

```mermaid
flowchart TD
    User["Discord 使用者"] -->|發送 /eat 或 /set| Bot["Discord Bot (discordgo)"]
    Bot -->|第一時間 (立即)| Defer["發送 Defer Response (思考中...)"]

    subgraph CoordinateResolution["坐標確認流程 (純坐標，無 Geocoding)"]
        CheckInput{"是否帶有即時經緯度坐標?"}
        CheckInput -- 是 (驗證成對合法) --> UseDirectCoord["採用即時坐標 (Lat, Lng)"]
        CheckInput -- 成對驗證失敗 (單一缺失/NaN) --> ReturnValError["回傳坐標成對與格式錯誤"]
        CheckInput -- 否 (兩者皆無) --> QueryUserPref["查詢 SQLite user_preferences"]
        QueryUserPref -- 查有紀錄 --> UsePrefCoord["採用偏好坐標與預設半徑 (標記預設位置)"]
        QueryUserPref -- 查無紀錄 --> ReturnHelpMsg["提示尚未設定預設坐標，請先使用 /set"]
    end

    Defer --> CoordinateResolution
    UseDirectCoord --> PlacesSearch["呼叫 Google Places API (New)"]
    UsePrefCoord --> PlacesSearch

    subgraph GooglePlaces["Google Places API (New) (Timeout: 10s, Retry: 429/5xx)"]
        PlacesSearch --> SearchAPI["searchText (textQuery: 關鍵字或「餐廳」, openNow: true, pageSize: 20, 矩形 locationRestriction, 僅載入第 1 頁)"]
    end

    SearchAPI --> FilterPhase["應用層過濾：餐飲類型 + currentOpeningHours.openNow == true + Haversine 距離 <= 半徑"]

    FilterPhase --> LotteryService["抽籤調度與暫存 (Lottery Service)"]

    subgraph MemoryCache["In-Memory Session 快取 (10 min TTL)"]
        LotteryService --> StoreCandidates["存入候選餐廳清單與下一頁 token (上限 1000 個 Session)"]
        StoreCandidates --> PickOne["隨機抽取 1 家"]
    end

    PickOne --> RenderEmbed["組裝 Discord Rich Embed + [🔄 再抽一家] 按鈕 (保護位置隱私)"]
    RenderEmbed --> UpdateInteraction["更新 Discord 訊息 (Followup/Edit)"]

    subgraph RerollAction["頻道成員點擊 [🔄 再抽一家] (頻道共同決策)"]
        ClickReroll["點擊按鈕"] --> CheckMemCache{"檢查 Session 暫存清單"}
        CheckMemCache -- 尚有未抽店家 --> PickNext["自記憶體抽取下一家 (零 API 費用)"]
        PickNext --> UpdateEmbed["更新原始 Embed 訊息"]
        CheckMemCache -- 本頁抽完且有下一頁 --> LoadNext["載入下一頁 (1 次 API，同 Session 同時僅一個請求)"]
        LoadNext --> PickNext
        CheckMemCache -- 全部抽完且無下一頁 --> ExhaustedMsg["提示候選餐廳已用罄，停用按鈕"]
        CheckMemCache -- 已過期/不存在 --> ExpiredMsg["提示清單已過期，請重新輸入 /eat"]
    end
```

---

## 3. 模組詳細規格

### 模組 1：組態與基礎設施（Config & Core）

- **環境變數載入**：
  - `DISCORD_TOKEN`：Discord Bot Token。
  - `DISCORD_APP_ID`：Discord Application ID（用於註冊 Slash Commands）。
  - `DISCORD_GUILD_ID`：指定測試伺服器 ID（選填，設定時指令可秒級生效）。
  - `GOOGLE_MAPS_API_KEY`：Google Cloud API 金鑰（需啟用 Places API New）。
  - `DB_PATH`：SQLite 資料庫檔案路徑（預設：`./data/random_eat.db`）。
- **日誌**：採用 Go 標準庫 `log/slog`。
- **生命週期管理**：支援優雅關閉（Graceful Shutdown），攔截 `SIGINT`/`SIGTERM` 釋放連線與清理快取資源。

---

### 模組 2：資料庫與持久化層（SQLite Persistence）

採用 `modernc.org/sqlite` 實作極輕量儲存層。

- **連線與效能最佳化（PRAGMA）**：
  ```sql
  PRAGMA journal_mode = WAL;
  PRAGMA cache_size = -2000; -- 限制快取約 2MB，節省 RAM
  PRAGMA foreign_keys = ON;
  PRAGMA synchronous = NORMAL;
  PRAGMA busy_timeout = 5000;
  ```
  - 連線池設定：`db.SetMaxOpenConns(1)`。

- **資料表設計（Schema）**：
  ```sql
  -- 使用者個人預設坐標與偏好
  CREATE TABLE IF NOT EXISTS user_preferences (
      user_id TEXT PRIMARY KEY,
      location_name TEXT NOT NULL DEFAULT '', -- 自訂別名 (如: 公司、家，僅用於 /set 私密回應)
      latitude REAL NOT NULL,
      longitude REAL NOT NULL,
      radius INTEGER NOT NULL DEFAULT 1000,
      updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );
  ```

- **Repository 介面與方法**：
  - `InitDB(dbPath string) (*sql.DB, error)`
  - `UpsertPreference(ctx, pref *UserPreference) error`
  - `GetPreference(ctx, userID string) (*UserPreference, error)`
  - `DeletePreference(ctx, userID string) error`

---

### 模組 3：Google Places API (New) 整合

直接以經緯度坐標呼叫 Google Places API (New)，無需經過 Geocoding。

- **HTTP Client 與健全重試機制**：
  - 超時時間：統一為 **10 秒**。
  - 重試策略：針對 429 (Too Many Requests)、500、502、503、504 與暫態網路連線異常進行最多 2 次重試（指數退避：100ms, 200ms）。
  - 不可重試錯誤：400、401、403、404 等客戶端錯誤立即中斷並回報，不進行無意義重試。
- **餐廳搜尋（searchText + openNow，延遲分頁；有無關鍵字共用）**：
  - 端點：`POST https://places.googleapis.com/v1/places:searchText`
  - `textQuery`：使用者輸入之關鍵字（如「拉麵」、「火鍋」）；未輸入時為「餐廳」。
  - 採用 Text Search 而非 Nearby Search 之原因：Nearby Search (New) 不支援 `openNow` 請求參數且最多僅回傳 20 筆、無分頁；Text Search 可由 API 端過濾營業中，並支援最多 3 頁（共 60 筆）。
  - Payload（searchText 之 `locationRestriction` 僅支援矩形，以涵蓋搜尋圓形之外接矩形（放大 1% 緩衝）硬性限制範圍）：
    ```json
    {
      "textQuery": "拉麵",
      "openNow": true,
      "pageSize": 20,
      "locationRestriction": {
        "rectangle": {
          "low":  { "latitude": 25.02481, "longitude": 121.55448 },
          "high": { "latitude": 25.04299, "longitude": 121.57432 }
        }
      }
    }
    ```
  - 餐飲類型過濾：透過 Field Mask 請求 `places.types`，僅保留 `restaurant`、`cafe`、`meal_takeaway`、`bakery` 或任何 `*_restaurant` 子類型之店家。
  - 不使用 `includedType`/`strictTypeFiltering`：限定單一類型會在搜尋「咖啡」、「麵包」等關鍵字時排除咖啡廳與麵包店，改由應用層餐飲類型過濾。
  - 延遲分頁（Lazy Pagination）：
    - `/eat` 僅載入第 1 頁；若第 1 頁過濾後為 0 筆且存在 `nextPageToken`，才繼續往下載入，避免誤判無結果。
    - `nextPageToken` 存入 Session；Reroll 抽完目前候選且仍有下一頁時，才使用 `pageToken` 載入下一頁（關鍵字與其餘參數與第 1 頁完全相同），新店家依 place ID 去重後附加至候選名單。
    - 同一 Session 同時僅允許一個請求載入下一頁，其他並行點擊回傳 `ErrLoadingMore`（「正在載入更多餐廳，請稍後再按一次」）；載入失敗時保留 token 供重試。
    - 最多 3 頁，達上限後視為無下一頁。
- **搜尋半徑保證（Haversine 距離過濾）**：
  - 透過矩形 `locationRestriction` 硬性限制範圍，後端再以 Haversine 公式依實際經緯度計算店家與搜尋中心直線大圓距離，剃除矩形四角超出圓形半徑之店家：
    $$\text{distance} \le \text{radiusMeters}$$
  - 超出半徑者全數剃除，保證 `/eat radius=X` 回傳店家絕不超出指定半徑。
- **營業中狀態過濾**：
  - 僅保留 API 明確回傳 `currentOpeningHours.openNow == true` 之店家；若為 `false` 或 `nil`/未知則一律排除，不以 regularOpeningHours 誤判即時營業狀態。
- **時區解析（Timezone Awareness）**：
  - 依據 API 回傳之 `places.timeZone`（IANA ID 如 `Asia/Taipei`）或 `places.utcOffsetMinutes` 解析店家當地時間，對應精確今日星期，非硬編碼單一時區。
- **Field Mask 與語系設定**：
  - Header `X-Goog-FieldMask`：
    - `places.id,places.types,places.displayName,places.formattedAddress,places.location,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel,places.currentOpeningHours,places.regularOpeningHours,places.utcOffsetMinutes,places.timeZone,nextPageToken`
  - Header `X-Goog-Language-Code: zh-TW`
  - Header `X-Goog-Api-Key: {GOOGLE_MAPS_API_KEY}`

---

### 模組 4：抽籤與暫存調度（Lottery Service）

- **坐標驗證邏輯**：
  - 成對性驗證：`latitude` 與 `longitude` 必須同時提供或同時省略；單獨提供其中之一將回傳驗證錯誤。
  - 數值合法性：排除 `NaN`、`+Inf`、`-Inf`。
  - 經緯度範圍限制：`-90.0 <= latitude <= 90.0`，`-180.0 <= longitude <= 180.0`。
  - 半徑範圍限制：`100 <= radius <= 5000`（公尺）。
- **隱私防護處理**：
  - 使用者在 `/set` 儲存的自訂名稱（如「公司」、「我家」）僅儲存於資料庫供個人識別。
  - `/eat` 公開卡片之搜尋中心一律標記為「預設位置」或「指定坐標」，絕不輸出私人別名。
- **候選名單記憶體快取（In-Memory Session Cache）**：
  - 保存每次查詢之候選餐廳清單與下一頁 token（每頁最多 20 家，延遲載入最多 3 頁）。
  - Session 數量上限 1000 筆：達上限時先清除過期 Session，仍滿則淘汰最早到期者。
  - 設定 10 分鐘 TTL，並具備背景清理 Goroutine（支援安全多次呼叫 `Stop()`）。
  - 記錄已抽取的店家 ID；重抽時自剩餘候選隨機挑選。
  - **非循環終止語意**：當所有候選皆已抽出且沒有下一頁時，回傳 `ErrNoMoreCandidates`（「沒有更多候選餐廳，請重新使用 /eat」），不無限循環重抽先前看過的店家。
- **隨機演算法**：使用 Go 標準庫 `math/rand/v2`。

---

### 模組 5：Discord 互動層（Commands & Handlers）

- **Slash Commands 參數規格**：
  1. `/set`（設定個人預設坐標）：
     - `latitude`（緯度，Number，必填，例如 `25.0339`）
     - `longitude`（經度，Number，必填，例如 `121.5644`）
     - `radius`（搜尋半徑_公尺，Integer，選填，預設 1000，範圍 100~5000）
     - `name`（位置別名，String，選填，最多 32 字，例如「公司」、「家」）
     - 回應模式：`MessageFlagsEphemeral`（私密訊息，保護隱私）。
  2. `/eat`（隨機抽餐廳）：
     - `keyword`（食物種類或關鍵字，String，選填，最多 50 字，例如「拉麵」）
     - `latitude`（緯度，Number，選填，需與 longitude 同時提供）
     - `longitude`（經度，Number，選填，需與 latitude 同時提供）
     - `radius`（搜尋半徑_公尺，Integer，選填，覆蓋預設半徑）
     - 回應模式：公開頻道訊息；失敗時刪除公開的 Deferred 訊息，改以 Ephemeral 訊息回報錯誤。
  3. `/unset`（刪除個人預設坐標）：
     - 回應模式：`MessageFlagsEphemeral`。
  4. `/eat-list`（測試用，列出完整候選名單）：
     - 參數同 `/eat`，與 `/eat` 共用搜尋邏輯，依距離排序，回應模式為 Ephemeral。
     - 僅限 `EAT_LIST_ALLOWED_USERS`（逗號分隔之 Discord 使用者 ID）內的使用者使用；其他使用者收到 Ephemeral 無權限提示且不呼叫 API。
     - 未設定 `EAT_LIST_ALLOWED_USERS` 時不註冊此指令。
  5. `/help`（指令教學與坐標取得方式說明）：
     - 說明如何從 Google 地圖 App/網頁版長按點選以複製經緯度坐標。
- **指令註冊**：使用 `ApplicationCommandBulkOverwrite` 完整取代既有指令集，停用或移除之指令不會殘留。
- **錯誤處理與穩定性**：
  - `HandleInteraction` 攔截 panic，避免單一互動錯誤導致程序崩潰。
  - 僅顯示預定義之使用者錯誤訊息；內部錯誤（DB、Google API 回應）只記錄於 log。
  - Embed 標題、說明、欄位與 footer 依 Discord 長度上限截斷，空值改用預設文字。
- **Interaction 逾時防禦**：
  - 收到指令第一時間發送 `InteractionResponseDeferredChannelMessageWithSource`。
  - 運算完成後呼叫 `InteractionResponseEdit` 更新為抽籤結果卡片。
- **Reroll 互動與權限語意**：
  - 按鈕規格：`CustomID: reroll:<session_id>`，`Style: PrimaryButton`。
  - 權限模型：**頻道共同決策**模式，任何能看到該訊息之成員皆可點擊重抽，卡片會即時更新並註明最新點擊者。
  - 當目前候選剩餘 0 家但仍有下一頁時，按鈕維持可點擊，標籤轉為「🔄 再抽一家 (將載入更多餐廳)」，提示下次點擊將呼叫 API。
  - 當候選名單剩餘 0 家且沒有下一頁時，按鈕自動標記為 `Disabled: true` 且標籤轉為「已無更多備選餐廳」。
  - 若已過期或無更多候選，以 Ephemeral 訊息提示原因，避免污染頻道。
- **Discord Rich Embed 卡片排版**：
  - **Title**：`🍽️ [餐廳名稱]`（附 Google Maps 超連結）
  - **Description**：`⭐ 4.5 (1,230 則評論) • 價位：$$ • 營業中 🟢`
  - **Fields**：
    - 📍 **地址**：`台北市信義區...`
    - ⏰ **營業時間**：`星期一: 11:00 – 21:00`
    - 🎯 **搜尋條件**：`搜尋中心：預設位置\n搜尋範圍：半徑 1000 公尺`
  - **Footer**：`由 @使用者 發起 • 剩餘 19 家備選餐廳`
  - **Color**：`0xF59E0B`（活力橘黃）。

---

## 4. 驗收標準清單

- [x] **Google Places API (New)**
  - [x] `SearchRestaurantsPage` 使用 `POST /v1/places:searchText`、`openNow: true` 與矩形 `locationRestriction`，單次僅載入一頁並回傳 `nextPageToken`；有無關鍵字共用
  - [x] 過濾非餐飲類型店家（不使用 `strictTypeFiltering`）
  - [x] 下一頁沿用第 1 頁之關鍵字與參數
  - [x] 後端實作 Haversine 距離過濾，保證不超出搜尋半徑
  - [x] 僅保留 `currentOpeningHours.openNow == true` 之店家（排除未知與打烊）
  - [x] 依據 API 時區動態解析今日營業時間，無全域寫死時區
  - [x] 實作 10 秒 Timeout 與 429/5xx 指數退避重試
- [x] **抽籤核心與快取**
  - [x] `latitude` 與 `longitude` 成對性驗證與 NaN/Inf 防護
  - [x] `/eat` 公開卡片不洩漏使用者私密別名
  - [x] Session Cache 抽完即止，不無限循環重複
  - [x] Session Cache `Stop()` 具備等冪性（sync.Once 防 panic）
  - [x] Session Cache 支援高並行存取安全性（零 Data Race）
  - [x] 延遲分頁：Reroll 抽完才載入下一頁，同 Session 並行點擊僅呼叫一次 API，失敗可重試
  - [x] Session 數量上限與淘汰策略
- [x] **Discord 互動層**
  - [x] Deferred Interaction 防 3 秒逾時
  - [x] Reroll 支援頻道共同決策，用罄且無下一頁時停用按鈕
  - [x] `/eat-list` 僅限允許名單使用者
  - [x] 字串參數長度限制與 Embed 長度截斷
  - [x] 內部錯誤不外洩給使用者
  - [x] 完整測試覆蓋各模組
