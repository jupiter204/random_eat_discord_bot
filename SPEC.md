# Discord 餐廳抽籤機器人（Go 語言）系統設計與實作規格書 (SPEC)

## 1. 專案概述

本專案為一個基於 **Go 語言** 開發的 Discord 餐廳抽籤機器人。使用者**直接輸入經緯度坐標**或使用儲存的個人預設坐標，隨機抽取附近營業中的餐廳。支援自訂搜尋半徑、餐點關鍵字搜尋，並提供免重複扣 Google API 額度的「再抽一次（Reroll）」互動按鈕。

### 核心設計理念

- **零 Geocoding 費用**：由使用者直接提供精確經緯度坐標（例如：Google 地圖長按複製的 `25.0339, 121.5644`），免去地址解析與 Geocoding API 費用及延遲。
- **極輕量與極低資源佔用**：純 Go SQLite 驅動，記憶體常駐約 5~10 MB。
- **零 API 浪費重抽機制**：批次取得候選餐廳並暫存於記憶體，按鈕重抽不消耗 API。

### 核心技術選型

- **程式語言**：Go 1.21+
- **Discord SDK**：`github.com/bwmarrin/discordgo`
- **資料庫**：純 Go SQLite 驅動 `modernc.org/sqlite`（無須 CGO）
- **地理資訊服務**：Google Places API (New)（僅使用 Places API，無須 Geocoding API）

---

## 2. 系統架構與工作流程

```mermaid
flowchart TD
    User["Discord 使用者"] -->|發送 /eat 或 /set| Bot["Discord Bot (discordgo)"]
    Bot -->|第一時間 (立即)| Defer["發送 Defer Response (思考中...)"]

    subgraph CoordinateResolution["坐標確認流程 (純坐標，無 Geocoding)"]
        CheckInput{"是否帶有即時經緯度坐標?"}
        CheckInput -- 是 (驗證範圍合法) --> UseDirectCoord["採用即時坐標 (Lat, Lng)"]
        CheckInput -- 否 --> QueryUserPref["查詢 SQLite user_preferences"]
        QueryUserPref -- 查有紀錄 --> UsePrefCoord["採用偏好坐標與預設半徑"]
        QueryUserPref -- 查無紀錄 --> ReturnHelpMsg["提示尚未設定預設坐標，請先使用 /set"]
    end

    Defer --> CoordinateResolution
    UseDirectCoord --> PlacesSearch["呼叫 Google Places API (New)"]
    UsePrefCoord --> PlacesSearch

    subgraph GooglePlaces["Google Places API (New)"]
        PlacesSearch --> TextOrNearby{"是否有指定食物關鍵字?"}
        TextOrNearby -- 無 (純附近) --> NearbyAPI["searchNearby (restaurant, openNow: true)"]
        TextOrNearby -- 有 (指定品項) --> TextAPI["searchText (locationBias, openNow: true)"]
    end

    NearbyAPI --> LotteryService["抽籤調度與暫存 (Lottery Service)"]
    TextAPI --> LotteryService

    subgraph MemoryCache["In-Memory Session 快取 (10 min TTL)"]
        LotteryService --> StoreCandidates["存入候選餐廳清單 (最多 20 家)"]
        StoreCandidates --> PickOne["隨機抽取 1 家"]
    end

    PickOne --> RenderEmbed["組裝 Discord Rich Embed + [🔄 再抽一次] 按鈕"]
    RenderEmbed --> UpdateInteraction["更新 Discord 訊息 (Followup/Edit)"]

    subgraph RerollAction["使用者點擊 [🔄 再抽一次]"]
        ClickReroll["點擊按鈕"] --> CheckMemCache{"檢查 Session 暫存清單"}
        CheckMemCache -- 尚有名單 --> PickNext["自記憶體抽取下一家 (零 API 費用)"]
        PickNext --> UpdateEmbed["更新原始 Embed 訊息"]
        CheckMemCache -- 已過期/抽完 --> ExpiredMsg["提示清單已過期，請重新輸入 /eat"]
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
- **生命週期管理**：支援優雅關閉（Graceful Shutdown），攔截 `SIGINT`/`SIGTERM` 釋放連線。

---

### 模組 2：資料庫與持久化層（SQLite Persistence）

採用 `modernc.org/sqlite` 實作極輕量儲存層。

- **連線與效能最佳化（PRAGMA）**：

  ```sql
  PRAGMA journal_mode = WAL;
  PRAGMA cache_size = -2000; -- 限制快取約 2MB，節省 RAM
  PRAGMA foreign_keys = ON;
  PRAGMA synchronous = NORMAL;
  ```
  - 連線池設定：`db.SetMaxOpenConns(1)`。

- **資料表設計（Schema）**：

  ```sql
  -- 使用者個人預設坐標與偏好
  CREATE TABLE IF NOT EXISTS user_preferences (
      user_id TEXT PRIMARY KEY,
      location_name TEXT NOT NULL DEFAULT '', -- 自訂別名 (如: 公司、家)
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

---

### 模組 3：Google Places API (New) 整合

直接以經緯度坐標呼叫 Google Places API (New)，無需經過 Geocoding。

- **共用 HTTP Client**：超時時間 8 秒，包含連線重試機制。
- **API 模式 A：純周邊探索（searchNearby）**
  - 端點：`POST https://places.googleapis.com/v1/places:searchNearby`
  - 適用：未輸入食物關鍵字，直接隨機抽附近營業中的餐廳。
  - Payload：
    ```json
    {
      "includedTypes": ["restaurant"],
      "openNow": true,
      "maxResultCount": 20,
      "locationRestriction": {
        "circle": {
          "center": { "latitude": 25.0339, "longitude": 121.5644 },
          "radius": 1000.0
        }
      }
    }
    ```
- **API 模式 B：特定餐點搜尋（searchText）**
  - 端點：`POST https://places.googleapis.com/v1/places:searchText`
  - 適用：使用者指定餐點/關鍵字（如「拉麵」、「牛肉麵」）。
  - Payload：
    ```json
    {
      "textQuery": "拉麵",
      "openNow": true,
      "locationBias": {
        "circle": {
          "center": { "latitude": 25.0339, "longitude": 121.5644 },
          "radius": 1000.0
        }
      }
    }
    ```
- **Field Mask 與語系設定（嚴格控管費用）**：
  - Header `X-Goog-FieldMask: places.id,places.displayName,places.formattedAddress,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel`
  - Header `X-Goog-Language-Code: zh-TW`
  - Header `X-Goog-Api-Key: {GOOGLE_MAPS_API_KEY}`

---

### 模組 4：抽籤與暫存調度（Lottery Service）

- **坐標驗證邏輯**：
  - 緯度範圍限制：`-90.0 <= latitude <= 90.0`
  - 經度範圍限制：`-180.0 <= longitude <= 180.0`
  - 半徑範圍限制：`100 <= radius <= 5000`（公尺）
- **候選名單記憶體快取（In-Memory Reroll Cache）**：
  - Google Places 單次最多回傳 20 家餐廳。
  - 以 Session ID（UUID）將候選名單暫存於記憶體，設定 10 分鐘 TTL。
  - 記錄已抽出的店家 ID，在同一個 Session 內重抽時優先挑選未被抽過的餐廳。
- **隨機演算法**：使用 Go 標準庫 `math/rand/v2`。

---

### 模組 5：Discord 互動層（Commands & Handlers）

- **Slash Commands 參數設計**：
  1. `/set`（設定個人預設坐標）：
     - `latitude`（緯度，Number/Float，必填，例如 `25.0339`）
     - `longitude`（經度，Number/Float，必填，例如 `121.5644`）
     - `radius`（搜尋半徑_公尺，Integer，選填，預設 1000）
     - `name`（位置別名，String，選填，例如「公司」、「家」）
     - 回應模式：`MessageFlagsEphemeral`（私密訊息，保護個人隱私）。
  2. `/eat`（隨機抽餐廳）：
     - `keyword`（食物種類或關鍵字，String，選填，例如「拉麵」）
     - `latitude`（緯度，Number/Float，選填，覆蓋預設坐標）
     - `longitude`（經度，Number/Float，選填，覆蓋預設坐標）
     - `radius`（搜尋半徑_公尺，Integer，選填，覆蓋預設半徑）
     - 回應模式：公開頻道訊息。
  3. `/help`（指令教學與坐標取得方式說明）：
     - 說明如何從 Google 地圖 App/網頁版長按點選以複製經緯度坐標。
- **Interaction 逾時防禦**：
  - 收到 Interaction 後，**立即（< 3 秒）** 發送 `InteractionResponseDeferredChannelMessageWithSource`。
  - 抽籤完成後呼叫 `FollowupMessageCreate` 或 `InteractionResponseEdit` 更新。
- **Message Component 按鈕互動**：
  - 在 Embed 訊息下方附加按鈕：
    - `CustomID`: `reroll:<session_id>`
    - `Label`: `🔄 再抽一次 (不消耗 API)`
    - `Style`: `PrimaryButton`
  - 點擊時自記憶體中抽出下一家，原地更新 Embed 卡片。
- **Discord Rich Embed 卡片排版**：
  - **Title**：`🍽️ [餐廳名稱]`（附 Google Maps 超連結）
  - **Description**：`⭐ 4.5 (1,230 則評論) • 價位：$$ • 營業中`
  - **Fields**：
    - 📍 **地址**：`台北市信義區...`
    - 🎯 **搜尋坐標與條件**：`坐標 (25.0339, 121.5644) • 半徑 1000m`（若有自訂別名則顯示別名）
  - **Footer**：`由 @使用者 發起抽籤 • 剩餘 19 家備選`
  - **Color**：`0xF59E0B`（橘黃色）。

---

## 4. 實作檢查清單（Checklist）

- [ ] **基礎建設**
  - [ ] 建立 `go.mod` 並引入 `discordgo` 與 `modernc.org/sqlite`
  - [ ] 實作 `config` 模組讀取環境變數與合法性檢查
  - [ ] 實作優雅關閉（Graceful Shutdown）
- [ ] **資料持久化（SQLite）**
  - [ ] 實作資料庫初始化與 `WAL` PRAGMA 設定
  - [ ] 實作 `user_preferences` 表與 CRUD 方法
- [ ] **Google Places API (New) 客戶端**
  - [ ] 實作 `searchNearby` API 呼叫（純坐標探索）
  - [ ] 實作 `searchText` API 呼叫（關鍵字搜尋）
  - [ ] 實作價位轉換與 `X-Goog-FieldMask` 欄位遮罩
- [ ] **抽籤核心服務**
  - [ ] 實作經緯度與半徑數值驗證（Validation）
  - [ ] 實作 Session In-Memory 暫存器（支援 TTL 自動回收）
  - [ ] 實作隨機抽取與排重邏輯
  - [ ] 實作 Fallback 邏輯（即時坐標 $\rightarrow$ DB 預設坐標 $\rightarrow$ 提示說明）
- [ ] **Discord 機器人層**
  - [ ] 註冊 `/set`, `/eat`, `/help` Slash Commands
  - [ ] 實作 Deferred Interaction 避免 3 秒逾時
  - [ ] 實作 Rich Embed 卡片渲染
  - [ ] 實作 `reroll` 按鈕點擊處理
- [ ] **測試與驗收**
  - [ ] 單元測試：坐標驗證、抽籤隨機性、TTL 暫存清理
  - [ ] 整合測試：Slash Commands 完整流程驗證
