# Roadmap - Discord 餐廳抽籤機器人

本文件記載 `random_eat_discord` 專案已完成之功能里程碑與未來規劃發展藍圖。

---

## 🎯 Completed (已完成項目)

### 1. 核心基礎建設與容器化
- [x] 純 Go 專案架構（Go 1.24+，零 CGO 依賴）
- [x] Podman / Docker 多階段容器化建置（Alpine Linux runtime）與 Rootless / SELinux `:Z` 支援
- [x] 組態管理與優雅關閉（Graceful Shutdown，攔截 SIGINT / SIGTERM 釋放連線）
- [x] SQLite 持久化儲存（`modernc.org/sqlite`，WAL 模式、單連線池與 2MB 快取限制）

### 2. Google Places API (New) 整合
- [x] 周邊探索真正呼叫 `searchNearby`（端點：`POST /v1/places:searchNearby`）
- [x] 關鍵字搜尋真正呼叫 `searchText`（端點：`POST /v1/places:searchText`）
- [x] 嚴格餐廳類別限定（`includedType: "restaurant"` 與 `strictTypeFiltering: true`）
- [x] 嚴格搜尋半徑保障（後端 Haversine 大圓距離計算，保證不超出指定半徑）
- [x] 明確營業中過濾（僅接受明確 `openNow: true`，排除未知與無資料店家）
- [x] 動態時區解析（依據 API 回傳之 `places.timeZone` 與 `places.utcOffsetMinutes` 計算營業星期）
- [x] HTTP 逾時與重試機制（10 秒 Timeout，針對 429 與 5xx 暫態錯誤實作指數退避重試）
- [x] 嚴格欄位遮罩（Field Mask 最佳化，兼顧過濾所需欄位與 API 成本控制）

### 3. 抽籤核心邏輯與工作階段快取
- [x] 經緯度座標成對性驗證（`latitude` 與 `longitude` 必須成對提供）與數值範圍/NaN/Inf 檢查
- [x] 搜尋半徑範圍校驗（100 ~ 5000 公尺）
- [x] 個人位置隱私保護（公開抽籤卡片不透露個人私人別名，僅顯示「預設位置」或「指定坐標」）
- [x] 記憶體 Session 快取（10 分鐘 TTL，定時背景回收且 Stop 具等冪安全）
- [x] 非循環重抽排重邏輯（候選抽完即終止，回傳候選用罄錯誤並停用按鈕）
- [x] 高並行安全防護（SessionCache 經 Race Detector 驗證無 Data Race）

### 4. Discord 互動層
- [x] Slash Commands 註冊（`/set`, `/eat`, `/help`）
- [x] 3 秒 Interaction 逾時防護（Deferred Response 機制）
- [x] `/set` Ephemeral 私密設定回應，保護住家與辦公室等敏感位置
- [x] `/eat` 公開卡片渲染（店名連結、評分、營業時間、價位標籤、不洩漏私密位置）
- [x] 頻道共同決策 Reroll 按鈕互動（零 API 費用，即時更新原卡片並標示發起者）
- [x] 候選抽完時自動停用「再抽一家」按鈕

### 5. 測試覆蓋
- [x] Google API Client 單元與 Mock 伺服器測試（包含各類狀態碼、重試、逾時、異常 JSON）
- [x] 抽籤服務單元測試（包含邊界座標、成對驗證、候選用罄、重抽排重、並行存取）
- [x] Discord Views 卡片渲染與隱私防護單元測試

---

## 🔮 Planned (未來規劃項目)

### 1. 互動與使用者體驗增強
- [ ] 支援 Discord 互動式下拉選單（例如：篩選特定價位區間 $ ~ $$$$）
- [ ] 支援依照 Google 評分下限篩選（例如：僅抽取評分 $\ge 4.0$ 的店家）
- [ ] 支援自訂排除黑名單餐廳（例如：使用者可標記不喜歡的店家）
- [ ] 支援投票多選模式（一次抽出 3 家發起頻道內 Emoji 或按鈕投票）

### 2. 架構與維運
- [ ] Discord 指令層頻率限制（Rate Limiting，防止惡意使用者刷指令）
- [ ] Prometheus Metrics 監控指標（API 呼叫次數、抽籤延遲、快取命中率）
- [ ] 分散式快取支援（例如可選 Redis 作為 Session Cache 後端以支援多執行個體部署）
- [ ] Docker 映像檔自動建置與 CI/CD GitHub Actions 工作流
