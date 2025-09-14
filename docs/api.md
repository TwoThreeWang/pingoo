# Pingoo API 使用文档

发布于 2025年9月14日 · 最后更新 2025年9月14日

## 基础信息

- **Base URL**: `http://localhost:8080`
- **API Base Path**: `/api`
- **Content-Type**: `application/json`

## 认证

系统采用JWT认证机制，所有需要认证的API端点都需要在请求头中添加：

```
Authorization: Bearer <your_jwt_token>
```

## API端点

### 1. 用户认证相关

#### 用户注册

**POST** `/api/auth/register`

**请求体**:
```json
{
  "email": "user@example.com",
  "password": "securepassword123"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "email": "user@example.com",
    "created_at": "2024-01-01T12:00:00Z"
  }
}
```

#### 用户登录

**POST** `/api/auth/login`

**请求体**:
```json
{
  "email": "user@example.com",
  "password": "securepassword123"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "user": {
      "id": 1,
      "email": "user@example.com",
      "created_at": "2024-01-01T12:00:00Z"
    }
  }
}
```

#### 获取当前用户信息

**GET** `/api/auth/me`

**请求头**: 需要JWT认证

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "email": "user@example.com",
    "created_at": "2024-01-01T12:00:00Z"
  }
}
```

#### 更新用户信息

**PUT** `/api/auth/me`

**请求头**: 需要JWT认证

**请求体**:
```json
{
  "email": "newemail@example.com"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "email": "newemail@example.com",
    "created_at": "2024-01-01T12:00:00Z"
  }
}
```

### 2. 站点管理相关

#### 创建站点

**POST** `/api/sites`

**Headers**:
```
Authorization: Bearer <token>
```

**请求体**:
```json
{
  "name": "我的网站",
  "url": "https://example.com",
  "description": "这是我的个人网站"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "name": "我的网站",
    "url": "https://example.com",
    "description": "这是我的个人网站",
    "tracking_code": "<script src='http://localhost:8080/static/pingoo.js' data-site-id='1'></script>",
    "user_id": 1,
    "created_at": "2024-01-01T12:00:00Z"
  }
}
```

#### 获取站点列表

**GET** `/api/sites`

**Headers**:
```
Authorization: Bearer <token>
```

**查询参数**:
- `page` (可选): 页码，默认1
- `page_size` (可选): 每页数量，默认10

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "list": [
      {
        "id": 1,
        "name": "我的网站",
        "url": "https://example.com",
        "description": "这是我的个人网站",
        "tracking_code": "<script src='http://localhost:8080/static/pingoo.js' data-site-id='1'></script>",
        "user_id": 1,
        "created_at": "2024-01-01T12:00:00Z"
      }
    ],
    "total": 1,
    "page": 1,
    "page_size": 10
  }
}
```

#### 获取站点详情

**GET** `/api/sites/:id`

**Headers**:
```
Authorization: Bearer <token>
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "name": "我的网站",
    "url": "https://example.com",
    "description": "这是我的个人网站",
    "tracking_code": "<script src='http://localhost:8080/static/pingoo.js' data-site-id='1'></script>",
    "user_id": 1,
    "created_at": "2024-01-01T12:00:00Z"
  }
}
```

#### 获取站点统计

**GET** `/api/sites/:id/stats`

**Headers**:
```
Authorization: Bearer <token>
```

**查询参数**:
- `start_date` (可选): 开始日期 (格式: 2006-01-02)
- `end_date` (可选): 结束日期 (格式: 2006-01-02)

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
    ],
    "daily_stats": [
      {
        "date": "2024-01-01",
        "page_views": 100,
        "unique_visitors": 50,
        "events": 120
      }
    ]
  }
}
```

#### 更新站点信息

**PUT** `/api/sites/:id`

**Headers**:
```
Authorization: Bearer <token>
```

**请求体**:
```json
{
  "name": "更新后的网站名称",
  "url": "https://new-example.com",
  "description": "更新后的描述"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "name": "更新后的网站名称",
    "url": "https://new-example.com",
    "description": "更新后的描述",
    "tracking_code": "<script src='http://localhost:8080/static/pingoo.js' data-site-id='1'></script>",
    "user_id": 1,
    "updated_at": "2024-01-02T12:00:00Z"
  }
}
```

#### 删除站点

**DELETE** `/api/sites/:id`

**Headers**:
```
Authorization: Bearer <token>
```

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": 1,
    "message": "站点已删除"
  }
}
```

## 事件追踪（需要站点权限）

### 1. 追踪页面浏览

**POST** `/api/track/page_view`

**Headers**:
```
X-Site-ID: site_abc123
```

**请求体**:
```json
{
  "session_id": "sess_123456",
  "user_id": "user_789",
  "url": "https://mywebsite.com/page",
  "title": "关于我们",
  "referrer": "https://google.com"
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success"
}
```

### 2. 追踪自定义事件

**POST** `/api/track/event`

**Headers**:
```
X-Site-ID: site_abc123
```

**请求体**:
```json
{
  "session_id": "sess_123456",
  "user_id": "user_789",
  "url": "https://mywebsite.com/page",
  "event_type": "button_click",
  "event_value": "提交按钮",
  "metadata": {"button_id": "submit-btn"}
}
```

**响应**:
```json
{
  "code": 200,
  "message": "success"
}
```

### 3. 获取事件列表

**GET** `/api/events`

**Headers**:
```
Authorization: Bearer <token>
```

**查询参数**:
- `site_id`: 站点ID (必填)
- `start_date`: 开始日期 (YYYY-MM-DD)
- `end_date`: 结束日期 (YYYY-MM-DD)
- `event_type`: 事件类型
- `page`: 页码，默认1
- `page_size`: 每页数量，默认20

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "list": [
      {
        "id": 1,
        "site_id": 1,
        "event_type": "page_view",
        "page_url": "https://example.com/about",
        "referrer": "https://google.com",
        "user_agent": "Mozilla/5.0...",
        "ip_address": "192.168.1.1",
        "session_id": "sess_abc123",
        "custom_data": {},
        "created_at": "2024-01-01T12:00:00Z"
      }
    ],
    "total": 100,
    "page": 1,
    "page_size": 20
  }
}
```

#### 获取实时事件

**GET** `/api/events/realtime`

**Headers**:
```
Authorization: Bearer <token>
```

**查询参数**:
- `site_id`: 站点ID (必填)

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": [
    {
      "id": 1,
      "event_type": "page_view",
      "page_url": "https://example.com/about",
      "created_at": "2024-01-01T12:00:00Z"
    }
  ]
}
```

### 3. 会话管理相关

#### 获取活跃会话

**GET** `/api/sessions/active`

**Headers**:
```
Authorization: Bearer <token>
```

**查询参数**:
- `site_id`: 站点ID (必填)
- `limit` (可选): 返回数量限制，默认50

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": [
    {
      "session_id": "sess_abc123",
      "first_event": "2024-01-01T12:00:00Z",
      "last_event": "2024-01-01T12:30:00Z",
      "event_count": 15,
      "pages": ["/", "/about", "/contact"],
      "duration": 1800
    }
  ]
}
```

#### 获取会话详情

**GET** `/api/sessions/:session_id`

**Headers**:
```
Authorization: Bearer <token>
```

**查询参数**:
- `site_id`: 站点ID (必填)

**响应**:
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "session_id": "sess_abc123",
    "user_id": "user_789",
    "events": [
      {
        "id": 1,
        "event_type": "page_view",
        "page_url": "https://example.com/about",
        "title": "关于我们",
        "created_at": "2024-01-01T12:00:00Z"
      }
    ],
    "total_events": 15,
    "duration": 1800,
    "start_time": "2024-01-01T12:00:00Z",
    "end_time": "2024-01-01T12:30:00Z"
  }
}
```

## 前端埋点代码

### 自动生成的追踪代码

在获取站点详情时，系统会返回自动生成的追踪代码，格式如下：

```html
<script>
(function() {
  const siteId = 'site_abc123';
  const apiUrl = 'http://localhost:8080';

  // 生成会话ID
  function generateSessionId() {
    return 'sess_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
  }

  // 获取或创建会话ID
  function getSessionId() {
    let sessionId = localStorage.getItem('pingoo_session_id');
    if (!sessionId) {
      sessionId = generateSessionId();
      localStorage.setItem('pingoo_session_id', sessionId);
    }
    return sessionId;
  }

  // 追踪页面浏览
  function trackPageView() {
    fetch(apiUrl + '/api/track/page_view', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Site-ID': siteId
      },
      body: JSON.stringify({
        session_id: getSessionId(),
        url: window.location.href,
        title: document.title,
        referrer: document.referrer
      })
    });
  }

  // 追踪自定义事件
  function trackEvent(eventType, eventValue, metadata) {
    fetch(apiUrl + '/api/track/event', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Site-ID': siteId
      },
      body: JSON.stringify({
        session_id: getSessionId(),
        url: window.location.href,
        event_type: eventType,
        event_value: eventValue,
        metadata: metadata || {}
      })
    });
  }

  // 页面加载时追踪
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', trackPageView);
  } else {
    trackPageView();
  }

  // 暴露全局函数
  window.pingoo = {
    track: trackEvent
  };
})();
</script>
```

## 错误处理

### 统一错误格式

所有API端点返回的错误响应都遵循以下格式：

```json
{
  "code": 400,
  "message": "请求参数验证失败",
  "error": {
    "email": "邮箱格式不正确",
    "password": "密码长度至少8位"
  }
}
```

### HTTP状态码说明

| 状态码 | 描述 |
|--------|------|
| `200` | 请求成功 |
| `400` | 请求参数错误 |
| `401` | 未授权访问 |
| `403` | 权限不足 |
| `404` | 资源不存在 |
| `500` | 服务器内部错误 |

### 常见错误响应示例

#### 参数验证错误 (400)
```json
{
  "code": 400,
  "message": "参数验证失败",
  "error": {
    "email": "邮箱格式不正确",
    "password": "密码长度至少8位"
  }
}
```

#### 未授权访问 (401)
```json
{
  "code": 401,
  "message": "请先登录"
}
```

#### 资源不存在 (404)
```json
{
  "code": 404,
  "message": "站点不存在"
}
```

#### 服务器错误 (500)
```json
{
  "code": 500,
  "message": "服务器内部错误"
}
```