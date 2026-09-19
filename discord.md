# Discord 餐廳抽籤機器人（Go 語言）核心模組實作細節

### 模組 1：Discord 互動層（Commands & Handlers）
* [ ] **Slash Command 註冊**：
  * **主抽籤指令**：`/eat [地點/關鍵字] [半徑_公尺]`（參數設為選填 Optional）。
  * **設定偏好指令**：`/set [地點/關鍵字] [半徑_公尺]`（儲存個人的預設搜尋中心與半徑）。
* [ ] **處理 Interaction 逾時問題（重要）**：
  * Discord 要求在 **3 秒** 內對 Interaction 做出回應，而資料庫查詢與 Google API 呼叫可能超過此時限。
  * **解法**：收到指令後，第一時間發送 `InteractionResponseDeferredChannelMessageWithSource`（顯示「思考中...」），待邏輯運算完成後，使用 `FollowupMessageCreate` 或 `InteractionResponseEdit` 更新為最終抽籤結果。

### 模組 2：使用者偏好與資料持久化層（SQLite）
* [ ] **技術選型與套件**：
  * 採用純 Go 實作驅動：`modernc.org/sqlite`（無須 CGO，跨平台編譯方便，記憶體常駐僅約 5~10 MB）。
* [ ] **低記憶體與效能最佳化（PRAGMA）**：
  * 啟用 WAL 模式提高並行讀取安全：`PRAGMA journal_mode = WAL;`
  * 限制頁面快取大小以維持極低 RAM 佔用：`PRAGMA cache_size = -2000;`（限制快取約 2MB）。
  * 設為單連線池（低頻情境下避免連線浪費）：`db.SetMaxOpenConns(1)`。
* [ ] **資料 Schema 設計**：
  ```sql
  CREATE TABLE IF NOT EXISTS user_preferences (
      user_id TEXT PRIMARY KEY,
      location_name TEXT NOT NULL,
      latitude REAL NOT NULL,
      longitude REAL NOT NULL,
      radius INTEGER NOT NULL DEFAULT 1000,
      updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );
  ```
  *(說明：直接儲存解析後的經緯度坐標，日後呼叫 `/eat` 時無須重複消耗 Google Geocoding API 額度)*
* [ ] **資料存取邏輯（Repository Pattern）**：
  * `InitDB(dbPath string) (*sql.DB, error)`：初始化連線並自動執行 `CREATE TABLE`。
  * `UpsertPreference(userID, locationName, lat, lng, radius)`：使用 `INSERT INTO ... ON CONFLICT(user_id) DO UPDATE`。
  * `GetPreference(userID) (*UserPreference, error)`：取得指定使用者預設值。

### 模組 3：Google Maps Places & Geocoding API 整合
* [ ] **地址轉坐標（Geocoding API）**：
  * 當使用者輸入純文字地名（例如「捷運市政府站」）時呼叫，轉換為 `lat, lng`。
  * 儲存於 SQLite 快取，後續搜尋可直接複用坐標。
* [ ] **Places API (New) 呼叫**：
  * 端點：`POST https://places.googleapis.com/v1/places:searchNearby`。
  * 參數設定：
    * `includedTypes`: `["restaurant"]`。
    * `openNow`: `true`（**確保只回傳目前營業中的店家**）。
    * `locationRestriction`: 以使用者指定或偏好設定之座標為中心與半徑。
* [ ] **Field Mask 設定（控管 API 費用）**：
  * 於 Header 加入 `X-Goog-FieldMask`，僅選取必要欄位：
    `places.displayName,places.formattedAddress,places.rating,places.userRatingCount,places.googleMapsUri,places.priceLevel`。

### 模組 4：抽籤與查詢調度邏輯（Business Logic）
* [ ] **參數 Fallback 與驗證流程**：
  * 檢查 `/eat` 是否帶有即時參數：
    * 若有帶入參數 $\rightarrow$ 透過 Geocoding 解析坐標後直接查詢。
    * 若無帶入參數 $\rightarrow$ 查詢 SQLite 取得該使用者的偏好坐標與半徑。
    * 若資料庫亦無紀錄 $\rightarrow$ 回傳友善提示（例如：「尚未設定預設位置，請先使用 `/set` 或在 `/eat` 後帶入地點」）。
* [ ] **防呆與邊界檢查**：
  * 檢查回傳列表是否為空（如深夜或偏遠地區無營業店家），並給予提示。
* [ ] **隨機抽取演算法**：
  * 使用 `crypto/rand` 或 `math/rand/v2` 隨機挑出 1 家（或多選備選名單）。
* [ ] **進階篩選（可擴充）**：
  * 依評分過濾（如星級 $\ge 4.0$）或依評分數做加權隨機。

### 模組 5：訊息排版與渲染（Discord Embed）
* [ ] **格式化呈現抽籤結果**：
  * 使用 Discord Rich Embed 格式化輸出：
    * **Title**：餐廳名稱（帶有 Google Maps 導航超連結）。
    * **Description**：星級評價（如 `⭐ 4.5 (1,230 則評論)`）、價位區間。
    * **Fields**：地址、搜尋中心依據（例如：使用預設位置「公司」）、半徑。
    * **Footer**：顯示觸發抽籤的使用者與抽籤時間。