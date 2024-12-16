FROM golang:1.21-alpine as builder

WORKDIR /app

# 设置Go环境变量
ENV GO111MODULE=on \
    GOPROXY=https://goproxy.cn,direct \
    CGO_ENABLED=0 \
    GOOS=linux

# 安装基本工具
RUN apk add --no-cache git tzdata

# 复制 go mod 文件
COPY go.mod go.sum ./

# 下载所有依赖
RUN go mod download
RUN go get github.com/eatmoreapple/openwechat

# 复制源代码（排除 .env）
COPY . .
# 复制 static 目录
COPY static /app/static

RUN rm -f .env

# 确保所有依赖正确处理
RUN go mod tidy

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o main .

FROM alpine

# 安装证书和时区数据
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/main .
# 从 builder 阶段复制 static 目录
COPY --from=builder /app/static /app/static

# 设置时区
ENV TZ=Asia/Shanghai



# 设置端口环境变量
ENV PORT=8080

CMD ["/app/main"]
