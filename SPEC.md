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
- **地理資訊服務**：Google Places API (New)（`searchNearby` & `searchText`）

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
        PlacesSearch --> TextOrNearby{"是否有指定食物關鍵字?"}
        TextOrNearby -- 無 (純附近) --> NearbyAPI["searchNearby (restaurant, maxResultCount: 20, locationRestriction)"]
        TextOrNearby -- 有 (指定品項) --> TextAPI["searchText (restaurant, strictTypeFiltering: true, openNow: true, locationBias)"]
    end

    NearbyAPI --> FilterPhase["應用層過濾：currentOpeningHours.openNow == true + Haversine 距離 <= 半徑"]
    TextAPI --> FilterPhase

    FilterPhase --> LotteryService["抽籤調度與暫存 (Lottery Service)"]

    subgraph MemoryCache["In-Memory Session 快取 (10 min TTL)"]
        LotteryService --> StoreCandidates["存入候選餐廳清單 (最多 20 家)"]
        StoreCandidates --> PickOne["隨機抽取 1 家"]
    end

    PickOne --> RenderEmbed["組裝 Discord Rich Embed + [🔄 再抽一家] 按鈕 (保護位置隱私)"]
    RenderEmbed --> UpdateInteraction["更新 Discord 訊息 (Followup/Edit)"]

    subgraph RerollAction["頻道成員點擊 [🔄 再抽一家] (頻道共同決策)"]
        ClickReroll["點擊按鈕"] --> CheckMemCache{"檢查 Session 暫存清單"}
        CheckMemCache -- 尚有未抽店家 --> PickNext["自記憶體抽取下一家 (零 API 費用)"]
        PickNext --> UpdateEmbed["更新原始 Embed 訊息"]
        CheckMemCache -- 候選名單抽完 --> ExhaustedMsg["提示候選餐廳已用罄，停用按鈕"]
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
- **API 模式 A：純周邊探索（searchNearby）**：
  - 端點：`POST https://places.googleapis.com/v1/places:searchNearby`
  - 適用：未輸入食物關鍵字，直接隨機抽附近營業中的餐廳。
  - Payload（**注意：Places API (New) 之 searchNearby Request 不支援且不包含 openNow 欄位**）：
    ```json
    {
      "includedTypes": ["restaurant"],
      "maxResultCount": 20,
      "locationRestriction": {
        "circle": {
          "center": { "latitude": 25.0339, "longitude": 121.5644 },
          "radius": 1000.0
        }
      }
    }
    ```
  - 營業狀態過濾：透過 Field Mask 請求 `places.currentOpeningHours`，回傳後由應用程式端過濾 `currentOpeningHours.openNow == true`。
- **API 模式 B：特定餐點搜尋（searchText）**：
  - 端點：`POST https://places.googleapis.com/v1/places:searchText`
  - 適用：使用者指定餐點/關鍵字（如「拉麵」、「火鍋」）。
  - Payload：
    ```json
    {
      "textQuery": "拉麵",
      "includedType": "restaurant",
      "strictTypeFiltering": true,
      "openNow": true,
      "locationBias": {
        "circle": {
          "center": { "latitude": 25.0339, "longitude": 121.5644 },
          "radius": 1000.0
        }
      }
    }
    ```
  - 分頁機制（Pagination）：若第 1 頁候選經半徑過濾後為 0 筆且存在 `nextPageToken`，最多使用 `pageToken` 額外抓取至第 2 頁，避免因 Google API 排序將遠處熱門店家排於首頁導致誤判無結果；若分頁請求失敗則將錯誤回傳上層，不靜默吞掉。
- **搜尋半徑保證（Haversine 距離過濾）**：
  - Nearby Search 透過 `locationRestriction` 限制圓形區域。
  - Text Search 透過 `locationBias` 搜尋候選，後端再以 Haversine 公式依實際經緯度計算店家與搜尋中心直線大圓距離：
    $$\text{distance} \le \text{radiusMeters}$$
  - 超出半徑者全數剃除，保證 `/eat radius=X` 回傳店家絕不超出指定半徑。
- **營業中狀態過濾**：
  - 僅保留 API 明確回傳 `currentOpeningHours.openNow == true` 之店家；若為 `false` 或 `nil`/未知則一律排除，不以 regularOpeningHours 誤判即時營業狀態。
- **時區解析（Timezone Awareness）**：
  - 依據 API 回傳之 `places.timeZone`（IANA ID 如 `Asia/Taipei`）或 `places.utcOffsetMinutes` 解析店家當地時間，對應精確今日星期，非硬編碼單一時區。
- **Field Mask 與語系設定**：
  - Header `X-Goog-FieldMask: places.id,places.displayName,places.formattedAddress,places.location,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel,places.currentOpeningHours,places.regularOpeningHours,places.utcOffsetMinutes,places.timeZone,nextPageToken`
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
  - 保存每次查詢之候選餐廳清單（最多 20 家）。
  - 設定 10 分鐘 TTL，並具備背景清理 Goroutine（支援安全多次呼叫 `Stop()`）。
  - 記錄已抽取的店家 ID；重抽時自剩餘候選隨機挑選。
  - **非循環終止語意**：當所有候選皆已抽出時，回傳 `ErrNoMoreCandidates`（「沒有更多候選餐廳，請重新使用 /eat」），不無限循環重抽先前看過的店家。
- **隨機演算法**：使用 Go 標準庫 `math/rand/v2`。

---

### 模組 5：Discord 互動層（Commands & Handlers）

- **Slash Commands 參數規格**：
  1. `/set`（設定個人預設坐標）：
     - `latitude`（緯度，Number，必填，例如 `25.0339`）
     - `longitude`（經度，Number，必填，例如 `121.5644`）
     - `radius`（搜尋半徑_公尺，Integer，選填，預設 1000，範圍 100~5000）
     - `name`（位置別名，String，選填，例如「公司」、「家」）
     - 回應模式：`MessageFlagsEphemeral`（私密訊息，保護隱私）。
  2. `/eat`（隨機抽餐廳）：
     - `keyword`（食物種類或關鍵字，String，選填，例如「拉麵」）
     - `latitude`（緯度，Number，選填，需與 longitude 同時提供）
     - `longitude`（經度，Number，選填，需與 latitude 同時提供）
     - `radius`（搜尋半徑_公尺，Integer，選填，覆蓋預設半徑）
     - 回應模式：公開頻道訊息。
  3. `/help`（指令教學與坐標取得方式說明）：
     - 說明如何從 Google 地圖 App/網頁版長按點選以複製經緯度坐標。
- **Interaction 逾時防禦**：
  - 收到指令第一時間發送 `InteractionResponseDeferredChannelMessageWithSource`。
  - 運算完成後呼叫 `InteractionResponseEdit` 更新為抽籤結果卡片。
- **Reroll 互動與權限語意**：
  - 按鈕規格：`CustomID: reroll:<session_id>`，`Style: PrimaryButton`。
  - 權限模型：**頻道共同決策**模式，任何能看到該訊息之成員皆可點擊重抽，卡片會即時更新並註明最新點擊者。
  - 當候選名單剩餘 0 家時，按鈕自動標記為 `Disabled: true` 且標籤轉為「已無更多備選餐廳」。
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
  - [x] `SearchNearbyRestaurants` 真正使用 `POST /v1/places:searchNearby`，Request 絕不含 `openNow` 欄位
  - [x] `SearchTextRestaurants` 使用 `POST /v1/places:searchText` 並限制 `includedType: "restaurant"` 與 `strictTypeFiltering: true`，支援最多 2 頁分頁過濾
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
- [x] **Discord 互動層**
  - [x] Deferred Interaction 防 3 秒逾時
  - [x] Reroll 支援頻道共同決策，用罄時停用按鈕
  - [x] 完整測試覆蓋各模組
