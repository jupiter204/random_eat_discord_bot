# 🍽️ Discord 餐廳抽籤機器人 (Eat What Bot)

基於 **Go 語言** 開發的高效、低資源佔用 Discord 餐廳抽籤機器人。整合 **Google Places API (New)**，透過 Slash Commands 隨機挑選附近**營業中**的餐廳，並支援零 API 費用的「🔄 再抽一家」按鈕、個人預設搜尋位置與搜尋半徑設定。

---

## ✨ 核心特色

- 🚀 **超低資源消耗**：純 Go 實作（無 CGO 依賴），SQLite 採用單連線與記憶體限制最佳化，常駐記憶體僅約 **5~10 MB**。
- 💸 **極致節省 API 費用**：
  - **純坐標定位**：使用者直接輸入經緯度，省去 Geocoding 地址轉換費用。
  - **批次暫存與重抽機制**：每次查詢最多獲取 20 家營業中餐廳並快取於記憶體（10 分鐘 TTL），點擊按鈕重抽**完全不消耗 Google API 額度**。
  - **嚴格欄位遮罩 (Field Mask)**：僅請求必要欄位，控管 Places API 呼叫成本。
- 🕒 **營業狀態保證**：預設僅過濾並回傳**當前營業中**的店家，避免抽到打烊餐廳。
- 🔒 **隱私防護**：`/set` 個人位置設定採用 **Ephemeral（私密訊息）**，保護住家/公司地址隱私。
- ⚡ **防 3 秒逾時機制**：完整支援 Discord Deferred Interaction，避免網路延遲導致指令失效。
- 🐳 **容器化設計**：專為 **Podman / Docker** 打造，提供 Rootless 與 SELinux `:Z` 支援。

---

## 🛠️ 技術選型

- **程式語言**：Go 1.24+
- **Discord SDK**：[`bwmarrin/discordgo`](https://github.com/bwmarrin/discordgo)
- **資料持久化**：[`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite)（純 Go 驅動，跨平台、零 CGO）
- **地理資訊服務**：Google Places API (New)（`searchNearby` & `searchText`）
- **容器環境**：Podman / Docker（基於 Alpine Linux，映像檔體積 < 25MB）

---

## 📖 指令說明 (Slash Commands)

| 指令    | 說明                             | 參數                                                                                                                                                     | 回應模式                 |
| :------ | :------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------- | :----------------------- |
| `/set`  | 設定個人的預設搜尋坐標與半徑     | `latitude` (必填/緯度)<br>`longitude` (必填/經度)<br>`radius` (選填/搜尋半徑公尺，預設 1000m)<br>`name` (選填/位置別名，如「公司」)                      | 🔒 私密訊息 (僅自己可見) |
| `/eat`  | 隨機抽取營業中的餐廳             | `keyword` (選填/指定餐點種類，如「拉麵」、「火鍋」)<br>`latitude` (選填/覆蓋預設緯度)<br>`longitude` (選填/覆蓋預設經度)<br>`radius` (選填/覆蓋預設半徑) | 📢 公開頻道訊息          |
| `/help` | 查看機器人功能教學與坐標取得方式 | 無                                                                                                                                                       | 🔒 私密訊息              |

> 💡 **如何取得經緯度坐標？**  
> 打開 Google 地圖 App 或網頁版，在目標位置**長按**（或按右鍵），即可一鍵複製如 `25.0339, 121.5644` 的經緯度坐標。

---

## ⚙️ 環境變數設定

在專案目錄下建立 `.env` 檔案：

```env
# Discord 機器人設定
DISCORD_TOKEN=your_discord_bot_token
DISCORD_APP_ID=your_discord_application_id
DISCORD_GUILD_ID= # 留空代表全域註冊；若填入特定伺服器 ID 則僅在該伺服器註冊

# Google Maps API 設定 (需在 Google Cloud Console 啟用 Places API New)
GOOGLE_MAPS_API_KEY=your_google_cloud_api_key

# SQLite 資料庫位置
DB_PATH=/app/data/random_eat.db
```

---

## 🚀 容器部署指南 (Podman / Docker)

本專案完全透過容器進行建置與部署，無論在 Linux 伺服器或 macOS (Mac mini M2) 上皆使用相同流程。

### 1. 建立資料目錄並構建映像檔

```bash
# 建立資料庫掛載目錄
mkdir -p data

# 構建輕量容器映像檔
podman build -t random_eat_bot:latest .
```

_(若使用 Docker，可將 `podman` 替換為 `docker`)_

### 2. 啟動容器（常駐背景運行）

```bash
podman run -d \
  --name random_eat_bot \
  --restart unless-stopped \
  --env-file .env \
  -e DB_PATH=/app/data/random_eat.db \
  -e TZ=Asia/Taipei \
  -v "$PWD"/data:/app/data:Z \
  random_eat_bot:latest
```

### 3. 檢查與維運指令

```bash
# 查看即時 Log 確認連線與指令註冊狀態
podman logs -f random_eat_bot

# 查看容器運行狀態
podman ps

# 重新啟動機器人
podman restart random_eat_bot

# 停止機器人
podman stop random_eat_bot
```

### 4. （選填）設定 Linux 開機自動啟動 (Systemd)

若伺服器為 Linux，可讓 Podman 容器交由 Systemd 託管實現開機自啟：

```bash
mkdir -p ~/.config/systemd/user/
podman generate systemd --name random_eat_bot --files --new
mv container-random_eat_bot.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now container-random_eat_bot.service
loginctl enable-linger $USER
```

---

## 📁 專案架構

```
random_eat_discord/
├── cmd/
│   └── bot/
│       └── main.go                 # 主程式進入點、信號攔截與 Graceful Shutdown
├── internal/
│   ├── config/
│   │   └── config.go              # 環境變數載入與驗證
│   ├── db/
│   │   ├── db.go                  # SQLite 初始化 (WAL、PRAGMA、單連線池)
│   │   ├── models.go              # UserPreference 資料模型
│   │   └── repository.go          # 使用者偏好 CRUD 實作
│   ├── googlemaps/
│   │   ├── client.go              # Places API (New) HTTP Client 封裝
│   │   └── models.go              # Places API 請求/回應與價位轉換模型
│   ├── lottery/
│   │   ├── service.go             # 抽籤核心邏輯、坐標驗證、Fallback 流程
│   │   ├── service_test.go        # 抽籤邏輯單元測試
│   │   └── session_cache.go       # 候選清單記憶體快取 (TTL 10m、重抽排重)
│   └── discord/
│       ├── bot.go                 # Discordgo 連線與 Slash Command 註冊
│       ├── commands.go            # /set, /eat, /help 指令規格定義
│       ├── handlers.go            # Interaction 路由與按鈕互動事件處理
│       └── views.go               # Discord Rich Embed 訊息卡片排版
├── data/                          # SQLite 資料庫儲存目錄 (.gitignore)
├── Dockerfile                     # 多階段輕量容器建置檔
├── compose.yaml                   # 生產環境 Podman Compose 設定
├── compose.dev.yaml               # 開發環境容器設定
├── SPEC.md                        # 詳細系統規格書
├── PLAN.md                        # 開發實作計畫書
└── README.md                      # 本說明文件
```

---

## 🧪 測試

在容器內執行單元測試：

```bash
podman run --rm -v "$PWD":/app:Z -w /app golang:1.24-alpine go test -v ./...
```

---

## 📄 License

本專案採用 [MIT License](LICENSE) 開源授權。
