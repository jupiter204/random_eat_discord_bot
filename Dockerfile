# Build Stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# 安裝基本編譯憑證與時區資訊
RUN apk add --no-cache git ca-certificates tzdata

# 優先複製依賴定義以利用快取
COPY go.mod go.sum* ./
RUN if [ -f go.sum ]; then go mod download; fi

# 複製原始碼
COPY . .

# 純 Go 靜態編譯（無須 CGO），並去除除錯資訊以縮小二進位檔體積
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/random_eat ./cmd/bot

# Final Minimal Runtime Stage
FROM alpine:3.21

# 安裝 HTTPS 憑證與時區資料
RUN apk add --no-cache ca-certificates tzdata && \
    mkdir -p /app/data

ENV TZ=Asia/Taipei

WORKDIR /app

# 從編譯階段複製執行檔
COPY --from=builder /build/random_eat /app/random_eat

# 宣告掛載點（用於存放 SQLite 資料庫）
VOLUME ["/app/data"]

ENTRYPOINT ["/app/random_eat"]
