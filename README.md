# Pingoo - 轻量的网站统计分析系统

基于Gin框架的轻量级网站统计分析系统，提供事件追踪、页面浏览统计和用户行为分析功能。

## 功能特性

- ✅ 事件追踪（页面浏览、自定义事件）
- ✅ 实时数据统计
- ✅ 多数据库支持（SQLite、MySQL、PostgreSQL）
- ✅ RESTful API设计
- ✅ 前端埋点支持
- ✅ 响应式API响应格式
- ✅ 用户注册和登录（JWT认证）
- ✅ 多站点管理
- ✅ 站点权限控制
- ✅ 站点统计代码生成

## 技术栈

- **后端框架**: Gin (Go)
- **数据库**: GORM（支持SQLite、MySQL、PostgreSQL）
- **配置管理**: 环境变量 + godotenv
- **API文档**: RESTful设计

## 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/yourusername/pingoo.git
cd pingoo
```

### 2. 环境要求

- Go 1.21+
- SQLite（默认）或 MySQL/PostgreSQL

### 3. 安装依赖

```bash
go mod download
```

### 4. 配置环境变量

复制 `.env.example` 为 `.env` 并根据需要修改配置：

```bash
cp .env.example .env
```

编辑 `.env` 文件，设置数据库配置：
```bash
# 数据库配置
DB_TYPE=sqlite  # 可选: sqlite, mysql, postgres
DB_HOST=localhost
DB_PORT=3306
DB_USER=root
DB_PASSWORD=password
DB_NAME=pingoo
DB_PATH=./data/pingoo.db  # 仅SQLite使用
PORT=8080
JWT_SECRET=your_jwt_secret_key
```

### 5. 运行项目

```bash
go run main.go
```

服务将在 `http://localhost:8080` 启动

### 6. 用户注册和登录

#### 通过API注册用户

```bash
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "securepassword123"
  }'
```

#### 用户登录获取Token

```bash
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "securepassword123"
  }'
```

### 7. 创建站点

使用获取到的JWT Token创建站点：

```bash
curl -X POST http://localhost:8080/api/sites \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -d '{
    "name": "我的网站",
    "url": "https://example.com",
    "description": "这是我的个人网站"
  }'
```

### 8. 获取追踪代码

创建站点后，你会收到包含追踪代码的响应：

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "name": "我的网站",
    "tracking_code": "<script src='http://localhost:8080/static/pingoo.js' data-site-id='1'></script>"
  }
}
```

### 9. 集成追踪代码

将收到的追踪代码添加到你的网站 `<head>` 标签内：

```html
<!DOCTYPE html>
<html>
<head>
    <script src='http://localhost:8080/static/pingoo.js' data-site-id='1'></script>
    <title>我的网站</title>
</head>
<body>
    <!-- 你的网站内容 -->
</body>
</html>
```

### 10. 验证追踪

访问你的网站，然后在Pingoo仪表盘中查看实时数据。

### 11. 使用Web界面

- 注册/登录: http://localhost:8080/login
- 仪表盘: http://localhost:8080/dashboard
- 站点管理: http://localhost:8080/websites
- API文档: http://localhost:8080/API.md

### 5. 测试API

#### 创建事件
```bash
curl -X POST http://localhost:8080/api/events \
  -H "Content-Type: application/json" \
  -d '{"session_id":"test_session","url":"https://example.com","event_type":"page_view","event_value":"首页"}'
```

#### 获取事件列表
```bash
curl -X GET http://localhost:8080/api/events
```

#### 页面浏览追踪
```bash
curl -X POST http://localhost:8080/api/track/page_view \
  -H "Content-Type: application/json" \
  -d '{"session_id":"test_session","url":"https://example.com","title":"测试页面"}'
```

#### 自定义事件追踪
```bash
curl -X POST http://localhost:8080/api/track/event \
  -H "Content-Type: application/json" \
  -d '{"session_id":"test_session","url":"https://example.com","event_type":"button_click","event_value":"提交按钮"}'
```

## 前端集成

### 基本集成步骤
1. 将追踪代码添加到网站HTML中
2. 使用提供的JavaScript函数进行事件追踪
3. 查看实时统计数据

### 配置方式
Pingoo支持两种配置方式：

#### 方式一：Script标签属性（推荐）
使用`tracking-id`（必填）和`user-id`（可选）属性：
```html
<script defer src="https://your-domain.com/pingoo.js" tracking-id="YOUR_TRACKING_ID" user-id="USER_ID"></script>
```

#### 方式二：全局变量配置
```html
<script>
    window.PINGOO_SITE_ID = 'YOUR_SITE_ID';
    window.PINGOO_API_URL = 'https://your-domain.com/api/';
</script>
<script src="https://your-domain.com/pingoo.js"></script>
```

### 示例HTML集成
```html
<!DOCTYPE html>
<html>
<head>
    <title>我的网站</title>
    <script defer src="https://your-domain.com/pingoo.js" tracking-id="YOUR_TRACKING_ID" user-id="USER_ID"></script>
</head>
<body>
    <h1>欢迎访问我的网站</h1>
    <button onclick="trackEvent('button_click', '首页按钮')">点击我</button>
</body>
</html>
```

### 高级集成示例

```html
<!DOCTYPE html>
<html>
<head>
    <title>电商网站</title>
    <script defer src="https://your-domain.com/pingoo.js" tracking-id="YOUR_TRACKING_ID" user-id="USER_ID"></script>
</head>
<body>
    <h1>产品页面</h1>
    <button onclick="trackEvent('product_view', '商品123')">查看商品</button>
    <button onclick="trackEvent('add_to_cart', '商品123')">加入购物车</button>
    <button onclick="trackEvent('purchase', '订单456')">立即购买</button>

    <script>
        // 页面加载时自动追踪
        trackPageView();

        // 追踪用户行为
        setTimeout(() => {
            trackEvent('time_on_page', '30_seconds');
        }, 30000);
    </script>
</body>
</html>
```

## API文档

详细API文档请参考 [API.md](API.md) 文件，包含以下主要内容：

- **用户认证API**：注册、登录、获取当前用户信息
- **站点管理API**：创建站点、获取站点列表、站点详情、站点统计
- **事件追踪API**：页面浏览追踪、自定义事件追踪
- **前端埋点代码**：自动生成的追踪代码集成指南
- **错误处理**：各种错误情况的响应格式

## 认证和权限

系统采用JWT认证机制，所有需要认证的API端点都需要在请求头中添加：

```
Authorization: Bearer <your_jwt_token>
```

## 快速开始API调用

### 1. 用户注册
```bash
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "securepassword123"
  }'
```

### 2. 用户登录
```bash
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "securepassword123"
  }'
```

### 3. 创建站点
```bash
curl -X POST http://localhost:8080/api/sites \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your_jwt_token>" \
  -d '{
    "name": "我的网站",
    "url": "https://mywebsite.com",
    "description": "我的个人网站"
  }'
```

### 4. 获取站点追踪代码
```bash
curl -X GET http://localhost:8080/api/sites/1 \
  -H "Authorization: Bearer <your_jwt_token>"
```

## 前端埋点示例

### JavaScript埋点代码

```javascript
// 页面浏览追踪
function trackPageView(url, title, referrer) {
  const sessionId = localStorage.getItem('session_id') || generateSessionId();
  const userId = localStorage.getItem('user_id') || '';

  fetch('http://localhost:8080/api/track/page_view', {
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

  fetch('http://localhost:8080/api/track/event', {
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
├── config/                 # 配置文件
│   └── config.go          # 配置结构体
├── controllers/           # 控制器层
│   ├── auth_controller.go  # 认证控制器（注册、登录、用户信息）
│   ├── event_controller.go # 事件控制器
│   ├── render.go          # 渲染控制器
│   ├── site_controller.go # 站点管理控制器
│   └── web_controller.go  # Web页面控制器
├── database/              # 数据库相关
│   ├── database.go        # 数据库连接
│   └── migrations.go      # 数据库迁移
├── middleware/            # 中间件
│   ├── auth.go            # JWT认证中间件
│   └── cors.go            # CORS中间件
├── models/                # 数据模型
│   ├── event.go           # 事件模型
│   ├── session.go         # 会话模型
│   ├── site.go            # 站点模型
│   ├── stats.go           # 统计模型
│   └── user.go            # 用户模型
├── routers/               # 路由配置
│   └── router.go          # 路由设置
├── services/              # 业务逻辑层
│   ├── event_service.go   # 事件服务
│   └── site_service.go    # 站点服务
├── utils/                 # 工具函数
│   ├── id_generator.go    # ID生成器
│   ├── response.go        # 响应工具
│   └── validator.go       # 验证器
├── templates/             # HTML模板
│   ├── base.html          # 基础模板
│   ├── index.html         # 首页
│   ├── dashboard.html     # 仪表盘
│   ├── login.html         # 登录页面
│   ├── register.html      # 注册页面
│   ├── profile.html       # 个人资料页面
│   ├── websites.html      # 站点管理页面
│   └── 404.html           # 404页面
├── public/                # 静态资源
│   ├── css/               # 样式文件
│   ├── js/                # JavaScript文件
│   ├── pingoo.js          # 前端埋点SDK
│   └── img/               # 图片资源
├── .env                   # 配置文件
├── .env.example           # 环境变量示例
├── go.mod                 # Go模块文件
├── go.sum                 # Go依赖锁定文件
├── README.md              # 项目说明
├── API.md                 # 详细API文档
├── LICENSE                # 许可证文件
├── Dockerfile             # Docker配置
├── docker-compose.yml     # Docker Compose配置
└── deploy.sh              # 部署脚本
```

## 配置说明

### 环境变量配置

在 `.env` 文件中配置以下参数：

```bash
# 服务器配置
PORT=8080
HOST=0.0.0.0

## 配置说明

项目使用环境变量进行配置，主要配置项如下：

### 数据库配置
- `DB_TYPE`: 数据库类型 (sqlite/mysql/postgres)，默认: sqlite
- `DB_HOST`: 数据库主机，默认: localhost
- `DB_PORT`: 数据库端口，默认: 5432 (PostgreSQL) 或 3306 (MySQL)
- `DB_USER`: 数据库用户
- `DB_PASSWORD`: 数据库密码
- `DB_NAME`: 数据库名称
- `DB_PATH`: SQLite数据库文件路径 (仅SQLite使用)，默认: ./data/pingoo.db

### JWT配置
- `JWT_SECRET`: JWT密钥，用于token签名 (必填)
- `JWT_EXPIRE_HOURS`: token过期时间(小时)，默认: 24

### 服务器配置
- `PORT`: 服务端口，默认: 8080
- `MODE`: 运行模式 (debug/release/test)，默认: debug

### 日志配置
- `LOG_LEVEL`: 日志级别 (debug/info/warn/error)，默认: info
- `LOG_FILE`: 日志文件路径，默认: ./logs/pingoo.log

### CORS配置
- `CORS_ALLOW_ORIGINS`: CORS允许的源，多个用逗号分隔，默认: *
- `CORS_ALLOW_METHODS`: 允许的HTTP方法，默认: GET,POST,PUT,DELETE,OPTIONS
- `CORS_ALLOW_HEADERS`: 允许的请求头，默认: Origin,Content-Type,Accept,Authorization

### 邮件配置 (可选)
- `SMTP_HOST`: SMTP服务器地址
- `SMTP_PORT`: SMTP服务器端口，默认: 587
- `SMTP_USER`: SMTP用户名
- `SMTP_PASSWORD`: SMTP密码
- `SMTP_FROM`: 发件人邮箱

### 示例配置文件

```bash
# 数据库配置
DB_TYPE=sqlite
DB_HOST=localhost
DB_PORT=3306
DB_USER=root
DB_PASSWORD=password
DB_NAME=pingoo
DB_PATH=./data/pingoo.db

# JWT配置
JWT_SECRET=your-super-secret-jwt-key-change-this-in-production
JWT_EXPIRE_HOURS=24

# 服务器配置
PORT=8080
MODE=debug

# 日志配置
LOG_LEVEL=info
LOG_FILE=./logs/pingoo.log

# CORS配置
CORS_ALLOW_ORIGINS=*
CORS_ALLOW_METHODS=GET,POST,PUT,DELETE,OPTIONS
CORS_ALLOW_HEADERS=Origin,Content-Type,Accept,Authorization

# 邮件配置 (可选)
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USER=your-email@gmail.com
SMTP_PASSWORD=your-app-password
SMTP_FROM=your-email@gmail.com
```
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
Description=Pingoo Service
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