# Pingoo Analytics - 网站统计分析系统

基于Gin框架的轻量级网站统计分析系统，提供事件追踪、页面浏览统计和用户行为分析功能。

## 功能特性

- ✅ 事件追踪（页面浏览、自定义事件）
- ✅ 实时数据统计
- ✅ 多数据库支持（SQLite、MySQL、PostgreSQL）
- ✅ RESTful API设计
- ✅ 前端埋点支持
- ✅ 响应式API响应格式

## 技术栈

- **后端框架**: Gin (Go)
- **数据库**: GORM（支持SQLite、MySQL、PostgreSQL）
- **配置管理**: 环境变量 + godotenv
- **API文档**: RESTful设计

## 快速开始

### 1. 环境要求

- Go 1.21+
- SQLite（默认）或 MySQL/PostgreSQL

### 2. 安装依赖

```bash
go mod download
```

### 3. 配置环境变量

复制 `.env.example` 为 `.env` 并根据需要修改配置：

```bash
cp .env.example .env
```

### 4. 运行项目

```bash
go run main.go
```

服务将在 `http://localhost:8080` 启动

## API文档

### 基础信息

- **Base URL**: `http://localhost:8080/api/v1`
- **Content-Type**: `application/json`

### 事件相关API

#### 1. 创建事件

**POST** `/api/v1/events`

**请求体**:
```json
{
  "session_id": "sess_123456",
  "user_id": "user_789",
  "url": "https://example.com/page",
  "referrer": "https://google.com",
  "user_agent": "Mozilla/5.0...",
  "event_type": "page_view",
  "event_value": "首页",
  "ip": "192.168.1.1"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "session_id": "sess_123456",
    "user_id": "user_789",
    "url": "https://example.com/page",
    "referrer": "https://google.com",
    "user_agent": "Mozilla/5.0...",
    "event_type": "page_view",
    "event_value": "首页",
    "ip": "192.168.1.1",
    "created_at": "2024-01-01T12:00:00Z"
  }
}
```

#### 2. 获取事件列表

**GET** `/api/v1/events`

**查询参数**:
- `page` (可选): 页码，默认1
- `page_size` (可选): 每页数量，默认20
- `user_id` (可选): 用户ID筛选
- `session_id` (可选): 会话ID筛选
- `event_type` (可选): 事件类型筛选
- `start_date` (可选): 开始日期 (格式: 2006-01-02)
- `end_date` (可选): 结束日期 (格式: 2006-01-02)

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "list": [
      {
        "id": 1,
        "session_id": "sess_123456",
        "user_id": "user_789",
        "url": "https://example.com/page",
        "referrer": "https://google.com",
        "user_agent": "Mozilla/5.0...",
        "event_type": "page_view",
        "event_value": "首页",
        "ip": "192.168.1.1",
        "created_at": "2024-01-01T12:00:00Z"
      }
    ],
    "total": 100,
    "page": 1,
    "page_size": 20
  }
}
```

#### 3. 获取事件统计

**GET** `/api/v1/events/stats`

**查询参数**: 同获取事件列表

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "total_events": 1000,
    "unique_visitors": 500,
    "page_views": 800,
    "bounce_rate": 0.25,
    "avg_session_duration": 180,
    "top_pages": [
      {"url": "/", "views": 500},
      {"url": "/about", "views": 200}
    ],
    "top_referrers": [
      {"referrer": "https://google.com", "count": 300},
      {"referrer": "https://facebook.com", "count": 150}
    ]
  }
}
```

#### 4. 获取单个事件

**GET** `/api/v1/events/:id`

**响应**: 同创建事件响应

### 追踪相关API

#### 1. 页面浏览追踪

**POST** `/api/v1/track/pageview`

**请求体**:
```json
{
  "session_id": "sess_123456",
  "user_id": "user_789",
  "url": "https://example.com/page",
  "referrer": "https://google.com",
  "title": "首页"
}
```

**响应**: 返回1x1透明GIF像素

#### 2. 自定义事件追踪

**POST** `/api/v1/track/event`

**请求体**:
```json
{
  "session_id": "sess_123456",
  "user_id": "user_789",
  "url": "https://example.com/page",
  "event_type": "button_click",
  "event_value": "提交按钮"
}
```

**响应**: 标准JSON响应

### 其他API

#### 健康检查

**GET** `/health`

**响应**:
```json
{
  "status": "ok",
  "message": "服务运行正常"
}
```

#### 根路径

**GET** `/`

**响应**:
```json
{
  "name": "Pingoo Analytics",
  "version": "1.0.0",
  "message": "网站统计分析系统API服务"
}
```

## 前端埋点示例

### JavaScript埋点代码

```javascript
// 页面浏览追踪
function trackPageView(url, title, referrer) {
  const sessionId = localStorage.getItem('session_id') || generateSessionId();
  const userId = localStorage.getItem('user_id') || '';

  fetch('http://localhost:8080/api/v1/track/pageview', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      session_id: sessionId,
      user_id: userId,
      url: url || window.location.href,
      referrer: referrer || document.referrer,
      title: title || document.title
    })
  });
}

// 自定义事件追踪
function trackEvent(eventType, eventValue) {
  const sessionId = localStorage.getItem('session_id') || generateSessionId();
  const userId = localStorage.getItem('user_id') || '';

  fetch('http://localhost:8080/api/v1/track/event', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      session_id: sessionId,
      user_id: userId,
      url: window.location.href,
      event_type: eventType,
      event_value: eventValue
    })
  });
}

// 生成会话ID
function generateSessionId() {
  const sessionId = 'sess_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
  localStorage.setItem('session_id', sessionId);
  return sessionId;
}

// 页面加载时自动追踪
window.addEventListener('load', function() {
  trackPageView();
});

// 点击事件追踪示例
document.addEventListener('click', function(e) {
  if (e.target.matches('button, .btn, a[href]')) {
    trackEvent('click', e.target.textContent || e.target.href);
  }
});
```

### HTML集成示例

```html
<!DOCTYPE html>
<html>
<head>
    <title>我的网站</title>
    <script src="/static/pingoo.js"></script>
</head>
<body>
    <h1>欢迎访问我的网站</h1>
    <button onclick="trackEvent('button_click', '提交按钮')">点击我</button>
</body>
</html>
```

## 数据库结构

### events表结构

```sql
CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(64),
    ip VARCHAR(64),
    url TEXT,
    referrer TEXT,
    user_agent TEXT,
    device VARCHAR(32),
    browser VARCHAR(32),
    os VARCHAR(32),
    screen VARCHAR(16),
    event_type VARCHAR(32),
    event_value TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_events_session_id ON events(session_id);
CREATE INDEX idx_events_user_id ON events(user_id);
CREATE INDEX idx_events_event_type ON events(event_type);
CREATE INDEX idx_events_created_at ON events(created_at);
```

## 项目结构

```
pingoo/
├── main.go                 # 主程序入口
├── .env                   # 配置文件
├── .env.example          # 配置示例
├── go.mod                 # Go模块文件
├── go.sum                 # Go依赖锁定文件
├── README.md              # 项目文档
├── controllers/           # 控制器目录
│   ├── event_controller.go
│   └── track_controller.go
├── models/                # 数据模型目录
│   └── event.go
├── routers/               # 路由配置目录
│   └── router.go
├── services/              # 业务逻辑目录
│   └── event_service.go
├── utils/                 # 工具函数目录
│   ├── response.go
│   └── validator.go
└── middleware/            # 中间件目录
    └── cors.go
```

## 配置说明

### 环境变量配置

在 `.env` 文件中配置以下参数：

```bash
# 服务器配置
PORT=8080
HOST=0.0.0.0

# 数据库配置
DB_TYPE=sqlite  # 可选: sqlite, mysql, postgres
DB_HOST=localhost
DB_PORT=3306
DB_USER=root
DB_PASSWORD=password
DB_NAME=pingoo
DB_PATH=./data/pingoo.db  # 仅SQLite使用

# 日志配置
LOG_LEVEL=info
LOG_FILE=./logs/pingoo.log

# CORS配置
CORS_ALLOW_ORIGINS=*
CORS_ALLOW_METHODS=GET,POST,PUT,DELETE,OPTIONS
CORS_ALLOW_HEADERS=Origin,Content-Type,Accept,Authorization
```

## 部署说明

### Docker部署

```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o pingoo .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/

COPY --from=builder /app/pingoo .
COPY --from=builder /app/.env.example ./.env

EXPOSE 8080
CMD ["./pingoo"]
```

### 生产环境部署

1. **使用systemd服务**:

```bash
# /etc/systemd/system/pingoo.service
[Unit]
Description=Pingoo Analytics Service
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/pingoo
ExecStart=/opt/pingoo/pingoo
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

2. **使用Nginx反向代理**:

```nginx
server {
    listen 80;
    server_name analytics.yourdomain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## 开发指南

### 添加新功能

1. **添加新API端点**:
   - 在 `routers/router.go` 中添加路由
   - 在 `controllers/` 中创建对应的控制器
   - 在 `services/` 中创建对应的业务逻辑

2. **添加新数据模型**:
   - 在 `models/` 中创建新的模型结构
   - 使用GORM标签定义数据库字段
   - 在 `main.go` 中自动迁移数据库

### 测试

```bash
# 运行所有测试
go test ./...

# 运行特定模块测试
go test ./controllers

# 生成测试覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 贡献指南

1. Fork项目
2. 创建功能分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 创建Pull Request

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

## 联系方式

- 项目地址: [https://github.com/TwoThreeWang/pingoo](https://github.com/TwoThreeWang/pingoo)
- 问题反馈: [Issues](https://github.com/TwoThreeWang/pingoo/issues)