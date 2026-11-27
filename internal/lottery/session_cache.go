package lottery

import (
	"math/rand/v2"
	"sync"
	"time"

	"random_eat_discord/internal/googlemaps"
)

// QueryContext 儲存該次抽籤的搜尋條件與來源說明
type QueryContext struct {
	Keyword       string
	Latitude      float64
	Longitude     float64
	Radius        int
	LocationName  string
	IsCustomCoord bool
	Initiator     string
}

// Session 代表一次抽籤的候選名單工作階段
type Session struct {
	ID        string
	Places    []googlemaps.Place
	PickedIDs map[string]bool
	QueryCtx  QueryContext
	ExpiresAt time.Time
	// NextPageToken 為 Google 下一頁的 token，空字串代表沒有更多頁
	NextPageToken string
	// PagesLoaded 已載入的頁數
	PagesLoaded int
	// FetchingPage 為 true 時代表有請求正在載入下一頁，防止多人同時點擊重複呼叫 API
	FetchingPage bool
}

// SessionCache 定義候選名單快取介面
type SessionCache interface {
	Store(sessionID string, places []googlemaps.Place, qCtx QueryContext, ttl time.Duration)
	Get(sessionID string) (*Session, bool)
	PickNext(sessionID string) (*googlemaps.Place, int, *QueryContext, error)
	// SetNextPage 記錄下一頁 token 與目前已載入頁數
	SetNextPage(sessionID, nextPageToken string, pagesLoaded int)
	// HasMorePages 回傳是否仍有下一頁可載入
	HasMorePages(sessionID string) bool
	// ClaimNextPage 取得下一頁 token 並標記為載入中；回傳 ErrSessionExpired、ErrNoMoreCandidates（無下一頁）或 ErrLoadingMore（他人載入中）
	ClaimNextPage(sessionID string) (token string, qCtx QueryContext, pagesLoaded int, err error)
	// CompleteNextPage 附加新一頁的餐廳（依 ID 去重）並更新 token，回傳實際新增筆數
	CompleteNextPage(sessionID string, places []googlemaps.Place, nextPageToken string) int
	// AbortNextPage 載入失敗時解除載入中標記，保留原 token 以便重試
	AbortNextPage(sessionID string)
	Delete(sessionID string)
	Stop()
}

// DefaultMaxSessions 記憶體中同時保留之 Session 數量上限
const DefaultMaxSessions = 1000

type inMemorySessionCache struct {
	mu          sync.RWMutex
	sessions    map[string]*Session
	maxSessions int
	stopChan    chan struct{}
	stopOnce    sync.Once
}

// NewSessionCache 建立 In-Memory 抽籤工作階段快取實例（上限 DefaultMaxSessions 筆）
func NewSessionCache() SessionCache {
	return NewSessionCacheWithLimit(DefaultMaxSessions)
}

// NewSessionCacheWithLimit 建立指定 Session 數量上限的快取；超過上限時優先淘汰已過期者，其次淘汰最早到期者
func NewSessionCacheWithLimit(maxSessions int) SessionCache {
	if maxSessions <= 0 {
		maxSessions = DefaultMaxSessions
	}
	c := &inMemorySessionCache{
		sessions:    make(map[string]*Session),
		maxSessions: maxSessions,
		stopChan:    make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

// Store 儲存批次候選清單
func (c *inMemorySessionCache) Store(sessionID string, places []googlemaps.Place, qCtx QueryContext, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.sessions[sessionID]; !exists && len(c.sessions) >= c.maxSessions {
		c.evictLocked(time.Now())
	}

	c.sessions[sessionID] = &Session{
		ID:        sessionID,
		Places:    places,
		PickedIDs: make(map[string]bool),
		QueryCtx:  qCtx,
		ExpiresAt: time.Now().Add(ttl),
	}
}

// evictLocked 騰出空間：先清除所有過期 Session，若仍達上限則淘汰最早到期的一筆（呼叫端須持有寫鎖）
func (c *inMemorySessionCache) evictLocked(now time.Time) {
	for id, s := range c.sessions {
		if now.After(s.ExpiresAt) {
			delete(c.sessions, id)
		}
	}
	if len(c.sessions) < c.maxSessions {
		return
	}

	var oldestID string
	var oldestExpiry time.Time
	for id, s := range c.sessions {
		if oldestID == "" || s.ExpiresAt.Before(oldestExpiry) {
			oldestID = id
			oldestExpiry = s.ExpiresAt
		}
	}
	delete(c.sessions, oldestID)
}

// Get 取得指定 Session（若不存在或已過期則回傳 false）
func (c *inMemorySessionCache) Get(sessionID string) (*Session, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s, exists := c.sessions[sessionID]
	if !exists || time.Now().After(s.ExpiresAt) {
		return nil, false
	}
	return s, true
}

// Delete 刪除指定 Session
func (c *inMemorySessionCache) Delete(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.sessions, sessionID)
}

// PickNext 隨機抽取一家尚未被抽過、且尚未打烊的餐廳，並回傳剩餘可抽數量。
// 當候選餐廳已全數抽完（或剩餘店家皆已打烊）時，回傳 ErrNoMoreCandidates，不進行無限制循環重複。
func (c *inMemorySessionCache) PickNext(sessionID string) (*googlemaps.Place, int, *QueryContext, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, exists := c.sessions[sessionID]
	if !exists || time.Now().After(s.ExpiresAt) {
		delete(c.sessions, sessionID)
		return nil, 0, nil, ErrSessionExpired
	}

	// 找出所有尚未抽過、且搜尋後尚未打烊的餐廳索引（同一 ID 只計一次）
	now := time.Now()
	var availableIndices []int
	seen := make(map[string]bool, len(s.Places))
	for idx, p := range s.Places {
		if s.PickedIDs[p.ID] || seen[p.ID] || p.IsClosedAt(now) {
			continue
		}
		seen[p.ID] = true
		availableIndices = append(availableIndices, idx)
	}

	// 若所有店家都已抽過，不重新循環，回傳候選用罄錯誤
	if len(availableIndices) == 0 {
		return nil, 0, nil, ErrNoMoreCandidates
	}

	// 隨機選出一筆尚未抽過的餐廳
	chosenIndex := availableIndices[rand.IntN(len(availableIndices))]
	chosenPlace := s.Places[chosenIndex]
	s.PickedIDs[chosenPlace.ID] = true

	// 剩餘數為「還能被抽出的店家數」，已打烊或重複的店家不計入，避免按鈕顯示可抽卻抽不到
	remaining := len(availableIndices) - 1
	qCtx := s.QueryCtx

	return &chosenPlace, remaining, &qCtx, nil
}

// SetNextPage 記錄下一頁 token 與已載入頁數
func (c *inMemorySessionCache) SetNextPage(sessionID, nextPageToken string, pagesLoaded int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if s, ok := c.sessions[sessionID]; ok {
		s.NextPageToken = nextPageToken
		s.PagesLoaded = pagesLoaded
	}
}

// HasMorePages 回傳是否仍有下一頁可載入
func (c *inMemorySessionCache) HasMorePages(sessionID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	s, ok := c.sessions[sessionID]
	return ok && time.Now().Before(s.ExpiresAt) && s.NextPageToken != ""
}

// ClaimNextPage 取得下一頁 token 並標記為載入中
func (c *inMemorySessionCache) ClaimNextPage(sessionID string) (string, QueryContext, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[sessionID]
	if !ok || time.Now().After(s.ExpiresAt) {
		delete(c.sessions, sessionID)
		return "", QueryContext{}, 0, ErrSessionExpired
	}
	if s.FetchingPage {
		return "", QueryContext{}, 0, ErrLoadingMore
	}
	if s.NextPageToken == "" {
		return "", QueryContext{}, 0, ErrNoMoreCandidates
	}

	s.FetchingPage = true
	return s.NextPageToken, s.QueryCtx, s.PagesLoaded, nil
}

// CompleteNextPage 附加新一頁餐廳（依 ID 去重），更新 token 並解除載入中標記
func (c *inMemorySessionCache) CompleteNextPage(sessionID string, places []googlemaps.Place, nextPageToken string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[sessionID]
	if !ok {
		return 0
	}

	existing := make(map[string]bool, len(s.Places))
	for _, p := range s.Places {
		existing[p.ID] = true
	}
	added := 0
	for _, p := range places {
		if !existing[p.ID] {
			existing[p.ID] = true
			s.Places = append(s.Places, p)
			added++
		}
	}

	s.NextPageToken = nextPageToken
	s.PagesLoaded++
	s.FetchingPage = false
	return added
}

// AbortNextPage 解除載入中標記，保留 token 以便重試
func (c *inMemorySessionCache) AbortNextPage(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if s, ok := c.sessions[sessionID]; ok {
		s.FetchingPage = false
	}
}

func (c *inMemorySessionCache) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now()
			for id, s := range c.sessions {
				if now.After(s.ExpiresAt) {
					delete(c.sessions, id)
				}
			}
			c.mu.Unlock()
		case <-c.stopChan:
			return
		}
	}
}

// Stop 停止背景清理 Goroutine（使用 sync.Once 保證多次呼叫安全不 panic）
func (c *inMemorySessionCache) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopChan)
	})
}
