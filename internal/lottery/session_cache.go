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
}

// SessionCache 定義候選名單快取介面
type SessionCache interface {
	Store(sessionID string, places []googlemaps.Place, qCtx QueryContext, ttl time.Duration)
	Get(sessionID string) (*Session, bool)
	PickNext(sessionID string) (*googlemaps.Place, int, *QueryContext, error)
	Delete(sessionID string)
	Stop()
}

type inMemorySessionCache struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	stopChan chan struct{}
	stopOnce sync.Once
}

// NewSessionCache 建立 In-Memory 抽籤工作階段快取實例
func NewSessionCache() SessionCache {
	c := &inMemorySessionCache{
		sessions: make(map[string]*Session),
		stopChan: make(chan struct{}),
	}
	go c.cleanupLoop()
	return c
}

// Store 儲存批次候選清單
func (c *inMemorySessionCache) Store(sessionID string, places []googlemaps.Place, qCtx QueryContext, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessions[sessionID] = &Session{
		ID:        sessionID,
		Places:    places,
		PickedIDs: make(map[string]bool),
		QueryCtx:  qCtx,
		ExpiresAt: time.Now().Add(ttl),
	}
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

// PickNext 隨機抽取一家尚未被抽過的餐廳，並回傳剩餘未抽數量。
// 當候選餐廳已全數抽完時，回傳 ErrNoMoreCandidates，不進行無限制循環重複。
func (c *inMemorySessionCache) PickNext(sessionID string) (*googlemaps.Place, int, *QueryContext, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, exists := c.sessions[sessionID]
	if !exists || time.Now().After(s.ExpiresAt) {
		delete(c.sessions, sessionID)
		return nil, 0, nil, ErrSessionExpired
	}

	// 找出所有尚未抽過的餐廳索引
	var availableIndices []int
	for idx, p := range s.Places {
		if !s.PickedIDs[p.ID] {
			availableIndices = append(availableIndices, idx)
		}
	}

	// 若所有店家都已抽過，不重新循環，回傳候選用罄錯誤
	if len(availableIndices) == 0 {
		return nil, 0, nil, ErrNoMoreCandidates
	}

	// 隨機選出一筆尚未抽過的餐廳
	chosenIndex := availableIndices[rand.IntN(len(availableIndices))]
	chosenPlace := s.Places[chosenIndex]
	s.PickedIDs[chosenPlace.ID] = true

	remaining := len(s.Places) - len(s.PickedIDs)
	qCtx := s.QueryCtx

	return &chosenPlace, remaining, &qCtx, nil
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
